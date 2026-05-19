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
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

type Encryptor interface {
	Encrypt(plaintext []byte) (string, error)
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
		ExpiresAt:   req.ExpiresAt,
	}
	if err := s.repo.Save(ctx, item); err != nil {
		return Secret{}, err
	}
	item.Ciphertext = ""
	return item, nil
}
