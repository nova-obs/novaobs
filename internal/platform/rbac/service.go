package rbac

type Service struct {
	repo Repository
}

func NewService(repo Repository) Service {
	return Service{repo: repo}
}

func (s Service) Authorize(subject Subject, req Request) Decision {
	bindings, err := s.repo.ListBindingsBySubject(subject.ID, subject.Type)
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
	return Decision{Allowed: false, Reason: "permission_denied"}
}

func matches(pattern string, value string) bool {
	return pattern == "*" || pattern == value
}

func scopeAllowed(mode string, policy Scope, req Scope) bool {
	if mode == "global" {
		return policy.Global
	}
	if policy.Global {
		return true
	}
	switch mode {
	case "cluster":
		return policy.ClusterID != "" && policy.ClusterID == req.ClusterID
	case "namespace":
		return policy.ClusterID != "" && policy.ClusterID == req.ClusterID &&
			policy.Namespace != "" && policy.Namespace == req.Namespace
	case "environment":
		return policy.Environment != "" && policy.Environment == req.Environment &&
			optionalScopeMatches(policy, req)
	case "service":
		return policy.ServiceID != "" && policy.ServiceID == req.ServiceID &&
			optionalScopeMatches(policy, req)
	default:
		return scopeContains(policy, req)
	}
}

func optionalScopeMatches(policy Scope, req Scope) bool {
	if policy.ClusterID != "" && policy.ClusterID != req.ClusterID {
		return false
	}
	if policy.Namespace != "" && policy.Namespace != req.Namespace {
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
	if policy.Environment != "" && policy.Environment != req.Environment {
		return false
	}
	if policy.ServiceID != "" && policy.ServiceID != req.ServiceID {
		return false
	}
	return policy.ClusterID != "" || policy.Namespace != "" || policy.Environment != "" || policy.ServiceID != ""
}
