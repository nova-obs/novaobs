package secret

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"io"
	"sort"
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

type Encryptor interface {
	Encrypt(plaintext []byte) (string, error)
	Decrypt(ciphertext string) ([]byte, error)
}

type AESGCMEncryptor struct {
	key []byte
}

func NewAESGCMEncryptor(key []byte) AESGCMEncryptor {
	copied := make([]byte, len(key))
	copy(copied, key)
	return AESGCMEncryptor{key: copied}
}

func (e AESGCMEncryptor) Encrypt(plaintext []byte) (string, error) {
	block, err := aes.NewCipher(e.key)
	if err != nil {
		return "", err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	sealed := aead.Seal(nonce, nonce, plaintext, nil)
	return base64.StdEncoding.EncodeToString(sealed), nil
}

func (e AESGCMEncryptor) Decrypt(ciphertext string) ([]byte, error) {
	sealed, err := base64.StdEncoding.DecodeString(ciphertext)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(e.key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	if len(sealed) < aead.NonceSize() {
		return nil, io.ErrUnexpectedEOF
	}
	nonce := sealed[:aead.NonceSize()]
	payload := sealed[aead.NonceSize():]
	return aead.Open(nil, nonce, payload, nil)
}

type Service struct {
	repo      Repository
	encryptor Encryptor
}

func NewService(repo Repository, encryptor Encryptor) Service {
	return Service{repo: repo, encryptor: encryptor}
}

func (s Service) Create(ctx context.Context, req CreateRequest) (Secret, error) {
	ciphertext, err := s.encryptor.Encrypt(req.Plaintext)
	if err != nil {
		return Secret{}, err
	}
	sum := sha256.Sum256(req.Plaintext)
	item := Secret{
		ID:          primitive.NewObjectID().Hex(),
		Name:        req.Name,
		Type:        req.Type,
		Scope:       req.Scope,
		Ciphertext:  ciphertext,
		Fingerprint: hex.EncodeToString(sum[:]),
		CreatedBy:   req.CreatedBy,
		CreatedAt:   time.Now().UTC(),
		RotatedAt:   req.RotatedAt,
		ExpiresAt:   req.ExpiresAt,
	}
	if err := s.repo.Save(ctx, item); err != nil {
		return Secret{}, err
	}
	item.Ciphertext = ""
	return item, nil
}

func (s Service) Plaintext(ctx context.Context, id string) ([]byte, Secret, error) {
	item, err := s.repo.Get(ctx, id)
	if err != nil {
		return nil, Secret{}, err
	}
	plaintext, err := s.encryptor.Decrypt(item.Ciphertext)
	if err != nil {
		return nil, Secret{}, err
	}
	item.Ciphertext = ""
	return plaintext, item, nil
}

func (s Service) PlaintextByTypeAndScope(ctx context.Context, typ string, scope Scope) ([]byte, Secret, error) {
	item, err := s.latestByTypeAndScope(ctx, typ, scope)
	if err != nil {
		return nil, Secret{}, err
	}
	plaintext, err := s.encryptor.Decrypt(item.Ciphertext)
	if err != nil {
		return nil, Secret{}, err
	}
	item.Ciphertext = ""
	return plaintext, item, nil
}

func (s Service) ListByType(ctx context.Context, typ string) ([]Secret, error) {
	items, err := s.repo.ListByType(ctx, typ)
	if err != nil {
		return nil, err
	}
	out := make([]Secret, len(items))
	for index, item := range items {
		item.Ciphertext = ""
		out[index] = item
	}
	return out, nil
}

func (s Service) latestByTypeAndScope(ctx context.Context, typ string, scope Scope) (Secret, error) {
	items, err := s.repo.ListByType(ctx, typ)
	if err != nil {
		return Secret{}, err
	}
	matched := make([]Secret, 0, len(items))
	for _, item := range items {
		if scopeMatches(item.Scope, scope) {
			matched = append(matched, item)
		}
	}
	if len(matched) == 0 {
		return Secret{}, ErrNotFound
	}
	sort.SliceStable(matched, func(left, right int) bool {
		return secretFreshnessTime(matched[left]).After(secretFreshnessTime(matched[right]))
	})
	return matched[0], nil
}

func secretFreshnessTime(item Secret) time.Time {
	if !item.RotatedAt.IsZero() {
		return item.RotatedAt
	}
	return item.CreatedAt
}
