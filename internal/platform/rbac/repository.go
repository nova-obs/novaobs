package rbac

type Repository interface {
	SaveRole(role Role) error
	GetRole(id string) (Role, error)
	SaveBinding(binding Binding) error
	ListBindingsBySubject(subjectID string, subjectType string) ([]Binding, error)
}
