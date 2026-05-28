package rbac

type SubjectResolver interface {
	ResolveSubjects(subject Subject) ([]Subject, error)
}

type Service struct {
	repo          Repository
	resolver      SubjectResolver
	superSubjects map[string]bool
}

type Option func(*Service)

func WithSubjectResolver(resolver SubjectResolver) Option {
	return func(s *Service) {
		s.resolver = resolver
	}
}

func WithSuperSubjects(subjects ...Subject) Option {
	return func(s *Service) {
		if s.superSubjects == nil {
			s.superSubjects = map[string]bool{}
		}
		for _, subject := range subjects {
			key := subjectKey(subject)
			if key == "" {
				continue
			}
			s.superSubjects[key] = true
		}
	}
}

func NewService(repo Repository, options ...Option) Service {
	service := Service{repo: repo}
	for _, option := range options {
		option(&service)
	}
	return service
}

func (s Service) Authorize(subject Subject, req Request) Decision {
	subjects, err := s.subjectsFor(subject)
	if err != nil {
		return Decision{Allowed: false, Reason: "subject_lookup_failed"}
	}
	for _, item := range subjects {
		if s.superSubjects[subjectKey(item)] {
			return Decision{Allowed: true}
		}
	}
	for _, item := range subjects {
		bindings, err := s.repo.ListBindingsBySubject(item.ID, item.Type)
		if err != nil {
			return Decision{Allowed: false, Reason: "permission_lookup_failed"}
		}
		for _, binding := range bindings {
			role, err := s.repo.GetRole(binding.RoleID)
			if err != nil {
				continue
			}
			for _, permission := range role.Permissions {
				if !matches(permission.Resource, req.Resource) || !matches(permission.Action, req.Action) {
					continue
				}
				if scopeAllowed(permission.ScopeMode, binding.Scope, req.Scope) {
					return Decision{Allowed: true}
				}
			}
		}
	}
	return Decision{Allowed: false, Reason: "permission_denied"}
}

func subjectKey(subject Subject) string {
	if subject.ID == "" || subject.Type == "" {
		return ""
	}
	return subject.Type + ":" + subject.ID
}

func (s Service) subjectsFor(subject Subject) ([]Subject, error) {
	out := []Subject{subject}
	if s.resolver == nil {
		return out, nil
	}
	related, err := s.resolver.ResolveSubjects(subject)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{subjectKey(subject): true}
	for _, item := range related {
		if item.ID == "" || item.Type == "" {
			continue
		}
		key := subjectKey(item)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, item)
	}
	return out, nil
}

func matches(pattern string, value string) bool {
	return pattern == "*" || pattern == value
}

func scopeAllowed(mode string, policy Scope, req Scope) bool {
	switch mode {
	case "global":
		return policy.Global
	case "cluster":
		if policy.Global {
			return true
		}
		return policy.ClusterID != "" && policy.ClusterID == req.ClusterID
	case "namespace":
		if policy.Global {
			return true
		}
		if policy.ClusterID == "" || policy.ClusterID != req.ClusterID {
			return false
		}
		if policy.AllNamespaces {
			return req.Namespace != ""
		}
		return namespaceAllowed(policy, req.Namespace)
	case "environment":
		if policy.Global {
			return true
		}
		return policy.Environment != "" && policy.Environment == req.Environment &&
			optionalScopeMatches(policy, req)
	case "service":
		if policy.Global {
			return true
		}
		return policy.ServiceID != "" && policy.ServiceID == req.ServiceID &&
			optionalScopeMatches(policy, req)
	default:
		return false
	}
}

func optionalScopeMatches(policy Scope, req Scope) bool {
	if policy.ClusterID != "" && policy.ClusterID != req.ClusterID {
		return false
	}
	if policy.Namespace != "" && policy.Namespace != req.Namespace {
		return false
	}
	if len(policy.Namespaces) > 0 && !containsString(policy.Namespaces, req.Namespace) {
		return false
	}
	if policy.Environment != "" && policy.Environment != req.Environment {
		return false
	}
	return true
}

func scopeContains(policy Scope, req Scope) bool {
	if policy.Global {
		return true
	}
	if policy.ClusterID != "" && policy.ClusterID != req.ClusterID {
		return false
	}
	if policy.Namespace != "" && policy.Namespace != req.Namespace {
		return false
	}
	if len(policy.Namespaces) > 0 && !containsString(policy.Namespaces, req.Namespace) {
		return false
	}
	if policy.Environment != "" && policy.Environment != req.Environment {
		return false
	}
	if policy.ServiceID != "" && policy.ServiceID != req.ServiceID {
		return false
	}
	return policy.ClusterID != "" || policy.Namespace != "" || len(policy.Namespaces) > 0 || policy.Environment != "" || policy.ServiceID != ""
}

func namespaceAllowed(policy Scope, namespace string) bool {
	if namespace == "" {
		return false
	}
	if policy.Namespace != "" {
		return policy.Namespace == namespace
	}
	return containsString(policy.Namespaces, namespace)
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
