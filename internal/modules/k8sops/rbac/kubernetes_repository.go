package rbac

import (
	"context"
	"sort"
	"strings"

	"novaapm/internal/platform/authctx"
	platformrbac "novaapm/internal/platform/rbac"

	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

type ClientsetProvider interface {
	Clientset(ctx context.Context, clusterID string) (kubernetes.Interface, error)
}

type KubernetesRepository struct {
	clients    ClientsetProvider
	authorizer Authorizer
}

func NewKubernetesRepository(clients ClientsetProvider, dependencies ...any) KubernetesRepository {
	repo := KubernetesRepository{clients: clients, authorizer: denyAuthorizer{}}
	for _, dependency := range dependencies {
		if value, ok := dependency.(Authorizer); ok && value != nil {
			repo.authorizer = value
		}
	}
	return repo
}

func (r KubernetesRepository) ListRoles(ctx context.Context, filter ListFilter) ([]RoleResource, error) {
	filter = normalizeListFilter(filter)
	if filter.ClusterID == "" {
		return nil, ErrInvalidRequest
	}
	if !r.allowed(ctx, filter.ClusterID, filter.Namespace) {
		return nil, ErrPermissionDenied
	}
	client, err := r.clients.Clientset(ctx, filter.ClusterID)
	if err != nil {
		return nil, err
	}
	if filter.Namespace == "" {
		result, err := client.RbacV1().ClusterRoles().List(ctx, metav1.ListOptions{})
		if err != nil {
			return nil, err
		}
		items := make([]RoleResource, 0, len(result.Items))
		for _, item := range result.Items {
			items = append(items, mapKubernetesClusterRole(filter.ClusterID, item))
		}
		sort.SliceStable(items, func(left, right int) bool { return items[left].Name < items[right].Name })
		return paginate(items, filter.Page, filter.PageSize), nil
	}
	result, err := client.RbacV1().Roles(filter.Namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	items := make([]RoleResource, 0, len(result.Items))
	for _, item := range result.Items {
		items = append(items, mapKubernetesRole(filter.ClusterID, item))
	}
	sort.SliceStable(items, func(left, right int) bool { return items[left].Name < items[right].Name })
	return paginate(items, filter.Page, filter.PageSize), nil
}

func (r KubernetesRepository) UpsertRole(ctx context.Context, item RoleResource) (RoleResource, error) {
	item = normalizeRoleResource(item)
	if item.ClusterID == "" || item.Name == "" || len(item.Rules) == 0 {
		return RoleResource{}, ErrInvalidRequest
	}
	client, err := r.clients.Clientset(ctx, item.ClusterID)
	if err != nil {
		return RoleResource{}, err
	}
	if item.Kind == "ClusterRole" && item.Namespace == "" {
		role := &rbacv1.ClusterRole{ObjectMeta: metav1.ObjectMeta{Name: item.Name}, Rules: policyRulesFromRules(item.Rules)}
		existing, err := client.RbacV1().ClusterRoles().Get(ctx, item.Name, metav1.GetOptions{})
		if err != nil {
			if !apierrors.IsNotFound(err) {
				return RoleResource{}, err
			}
			created, createErr := client.RbacV1().ClusterRoles().Create(ctx, role, metav1.CreateOptions{})
			if createErr != nil {
				return RoleResource{}, createErr
			}
			return mapKubernetesClusterRole(item.ClusterID, *created), nil
		}
		role.ResourceVersion = existing.ResourceVersion
		role.UID = existing.UID
		updated, err := client.RbacV1().ClusterRoles().Update(ctx, role, metav1.UpdateOptions{})
		if err != nil {
			return RoleResource{}, err
		}
		return mapKubernetesClusterRole(item.ClusterID, *updated), nil
	}
	if item.Namespace == "" || item.Kind != "Role" {
		return RoleResource{}, ErrInvalidRequest
	}
	role := &rbacv1.Role{
		ObjectMeta: metav1.ObjectMeta{Name: item.Name, Namespace: item.Namespace},
		Rules:      policyRulesFromRules(item.Rules),
	}
	existing, err := client.RbacV1().Roles(item.Namespace).Get(ctx, item.Name, metav1.GetOptions{})
	if err != nil {
		if !apierrors.IsNotFound(err) {
			return RoleResource{}, err
		}
		created, createErr := client.RbacV1().Roles(item.Namespace).Create(ctx, role, metav1.CreateOptions{})
		if createErr != nil {
			return RoleResource{}, createErr
		}
		return mapKubernetesRole(item.ClusterID, *created), nil
	}
	role.ResourceVersion = existing.ResourceVersion
	role.UID = existing.UID
	updated, err := client.RbacV1().Roles(item.Namespace).Update(ctx, role, metav1.UpdateOptions{})
	if err != nil {
		return RoleResource{}, err
	}
	return mapKubernetesRole(item.ClusterID, *updated), nil
}

func (r KubernetesRepository) DeleteRole(ctx context.Context, req DeleteRequest) (RoleResource, error) {
	req = normalizeDeleteRequest(req)
	if req.ClusterID == "" || req.Name == "" || req.UID == "" {
		return RoleResource{}, ErrInvalidRequest
	}
	client, err := r.clients.Clientset(ctx, req.ClusterID)
	if err != nil {
		return RoleResource{}, err
	}
	if req.Kind == "ClusterRole" && req.Namespace == "" {
		existing, err := client.RbacV1().ClusterRoles().Get(ctx, req.Name, metav1.GetOptions{})
		if err != nil {
			return RoleResource{}, err
		}
		if string(existing.UID) != req.UID {
			return RoleResource{}, ErrNotFound
		}
		deleted := mapKubernetesClusterRole(req.ClusterID, *existing)
		if err := client.RbacV1().ClusterRoles().Delete(ctx, req.Name, metav1.DeleteOptions{}); err != nil {
			return RoleResource{}, err
		}
		return deleted, nil
	}
	if req.Namespace == "" || req.Kind != "Role" {
		return RoleResource{}, ErrInvalidRequest
	}
	existing, err := client.RbacV1().Roles(req.Namespace).Get(ctx, req.Name, metav1.GetOptions{})
	if err != nil {
		return RoleResource{}, err
	}
	if string(existing.UID) != req.UID {
		return RoleResource{}, ErrNotFound
	}
	deleted := mapKubernetesRole(req.ClusterID, *existing)
	if err := client.RbacV1().Roles(req.Namespace).Delete(ctx, req.Name, metav1.DeleteOptions{}); err != nil {
		return RoleResource{}, err
	}
	return deleted, nil
}

func (r KubernetesRepository) ListBindings(ctx context.Context, filter ListFilter) ([]BindingResource, error) {
	filter = normalizeListFilter(filter)
	if filter.ClusterID == "" {
		return nil, ErrInvalidRequest
	}
	if !r.allowed(ctx, filter.ClusterID, filter.Namespace) {
		return nil, ErrPermissionDenied
	}
	client, err := r.clients.Clientset(ctx, filter.ClusterID)
	if err != nil {
		return nil, err
	}
	if filter.Namespace == "" {
		result, err := client.RbacV1().ClusterRoleBindings().List(ctx, metav1.ListOptions{})
		if err != nil {
			return nil, err
		}
		items := make([]BindingResource, 0, len(result.Items))
		for _, item := range result.Items {
			items = append(items, mapKubernetesClusterBinding(filter.ClusterID, item))
		}
		sort.SliceStable(items, func(left, right int) bool { return items[left].Name < items[right].Name })
		return paginate(items, filter.Page, filter.PageSize), nil
	}
	result, err := client.RbacV1().RoleBindings(filter.Namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	items := make([]BindingResource, 0, len(result.Items))
	for _, item := range result.Items {
		items = append(items, mapKubernetesBinding(filter.ClusterID, item))
	}
	sort.SliceStable(items, func(left, right int) bool { return items[left].Name < items[right].Name })
	return paginate(items, filter.Page, filter.PageSize), nil
}

func (r KubernetesRepository) UpsertBinding(ctx context.Context, item BindingResource) (BindingResource, error) {
	item = normalizeBindingResource(item)
	if item.ClusterID == "" || item.Name == "" || item.RoleRef.Name == "" {
		return BindingResource{}, ErrInvalidRequest
	}
	client, err := r.clients.Clientset(ctx, item.ClusterID)
	if err != nil {
		return BindingResource{}, err
	}
	if item.Kind == "ClusterRoleBinding" && item.Namespace == "" {
		binding := &rbacv1.ClusterRoleBinding{
			ObjectMeta: metav1.ObjectMeta{Name: item.Name},
			RoleRef:    rbacv1.RoleRef{APIGroup: "rbac.authorization.k8s.io", Kind: defaultRoleRefKind(item.RoleRef.Kind), Name: item.RoleRef.Name},
			Subjects:   subjectsFromModel("", item.Subjects),
		}
		existing, err := client.RbacV1().ClusterRoleBindings().Get(ctx, item.Name, metav1.GetOptions{})
		if err != nil {
			if !apierrors.IsNotFound(err) {
				return BindingResource{}, err
			}
			created, createErr := client.RbacV1().ClusterRoleBindings().Create(ctx, binding, metav1.CreateOptions{})
			if createErr != nil {
				return BindingResource{}, createErr
			}
			return mapKubernetesClusterBinding(item.ClusterID, *created), nil
		}
		binding.ResourceVersion = existing.ResourceVersion
		binding.UID = existing.UID
		updated, err := client.RbacV1().ClusterRoleBindings().Update(ctx, binding, metav1.UpdateOptions{})
		if err != nil {
			return BindingResource{}, err
		}
		return mapKubernetesClusterBinding(item.ClusterID, *updated), nil
	}
	if item.Namespace == "" || item.Kind != "RoleBinding" {
		return BindingResource{}, ErrInvalidRequest
	}
	binding := &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{Name: item.Name, Namespace: item.Namespace},
		RoleRef:    rbacv1.RoleRef{APIGroup: "rbac.authorization.k8s.io", Kind: defaultRoleRefKind(item.RoleRef.Kind), Name: item.RoleRef.Name},
		Subjects:   subjectsFromModel(item.Namespace, item.Subjects),
	}
	existing, err := client.RbacV1().RoleBindings(item.Namespace).Get(ctx, item.Name, metav1.GetOptions{})
	if err != nil {
		if !apierrors.IsNotFound(err) {
			return BindingResource{}, err
		}
		created, createErr := client.RbacV1().RoleBindings(item.Namespace).Create(ctx, binding, metav1.CreateOptions{})
		if createErr != nil {
			return BindingResource{}, createErr
		}
		return mapKubernetesBinding(item.ClusterID, *created), nil
	}
	binding.ResourceVersion = existing.ResourceVersion
	binding.UID = existing.UID
	updated, err := client.RbacV1().RoleBindings(item.Namespace).Update(ctx, binding, metav1.UpdateOptions{})
	if err != nil {
		return BindingResource{}, err
	}
	return mapKubernetesBinding(item.ClusterID, *updated), nil
}

func (r KubernetesRepository) DeleteBinding(ctx context.Context, req DeleteRequest) (BindingResource, error) {
	req = normalizeDeleteRequest(req)
	if req.ClusterID == "" || req.Name == "" || req.UID == "" {
		return BindingResource{}, ErrInvalidRequest
	}
	client, err := r.clients.Clientset(ctx, req.ClusterID)
	if err != nil {
		return BindingResource{}, err
	}
	if req.Kind == "ClusterRoleBinding" && req.Namespace == "" {
		existing, err := client.RbacV1().ClusterRoleBindings().Get(ctx, req.Name, metav1.GetOptions{})
		if err != nil {
			return BindingResource{}, err
		}
		if string(existing.UID) != req.UID {
			return BindingResource{}, ErrNotFound
		}
		deleted := mapKubernetesClusterBinding(req.ClusterID, *existing)
		if err := client.RbacV1().ClusterRoleBindings().Delete(ctx, req.Name, metav1.DeleteOptions{}); err != nil {
			return BindingResource{}, err
		}
		return deleted, nil
	}
	if req.Namespace == "" || req.Kind != "RoleBinding" {
		return BindingResource{}, ErrInvalidRequest
	}
	existing, err := client.RbacV1().RoleBindings(req.Namespace).Get(ctx, req.Name, metav1.GetOptions{})
	if err != nil {
		return BindingResource{}, err
	}
	if string(existing.UID) != req.UID {
		return BindingResource{}, ErrNotFound
	}
	deleted := mapKubernetesBinding(req.ClusterID, *existing)
	if err := client.RbacV1().RoleBindings(req.Namespace).Delete(ctx, req.Name, metav1.DeleteOptions{}); err != nil {
		return BindingResource{}, err
	}
	return deleted, nil
}

func (r KubernetesRepository) allowed(ctx context.Context, clusterID string, namespace string) bool {
	subject, _ := authctx.SubjectFrom(ctx)
	decision := r.authorizer.Authorize(subject, platformrbac.Request{
		Resource: "k8s.rbac",
		Action:   "read",
		Scope:    platformrbac.Scope{ClusterID: clusterID, Namespace: namespace},
	})
	return decision.Allowed
}

func normalizeListFilter(filter ListFilter) ListFilter {
	filter.ClusterID = strings.TrimSpace(filter.ClusterID)
	filter.Namespace = strings.TrimSpace(filter.Namespace)
	return filter
}

func mapKubernetesRole(clusterID string, item rbacv1.Role) RoleResource {
	rules := make([]Rule, 0, len(item.Rules))
	for _, rule := range item.Rules {
		rules = append(rules, Rule{
			APIGroups: append([]string(nil), rule.APIGroups...),
			Resources: append([]string(nil), rule.Resources...),
			Verbs:     append([]string(nil), rule.Verbs...),
		})
	}
	return RoleResource{
		ID:        string(item.UID),
		ClusterID: clusterID,
		Namespace: item.Namespace,
		Kind:      "Role",
		Name:      item.Name,
		UID:       string(item.UID),
		Rules:     rules,
		Source:    "Kubernetes API",
		UpdatedAt: item.CreationTimestamp.Time,
	}
}

func mapKubernetesClusterRole(clusterID string, item rbacv1.ClusterRole) RoleResource {
	rules := make([]Rule, 0, len(item.Rules))
	for _, rule := range item.Rules {
		rules = append(rules, Rule{
			APIGroups: append([]string(nil), rule.APIGroups...),
			Resources: append([]string(nil), rule.Resources...),
			Verbs:     append([]string(nil), rule.Verbs...),
		})
	}
	return RoleResource{
		ID:        string(item.UID),
		ClusterID: clusterID,
		Kind:      "ClusterRole",
		Name:      item.Name,
		UID:       string(item.UID),
		Rules:     rules,
		Source:    "Kubernetes API",
		UpdatedAt: item.CreationTimestamp.Time,
	}
}

func mapKubernetesBinding(clusterID string, item rbacv1.RoleBinding) BindingResource {
	subjects := make([]Subject, 0, len(item.Subjects))
	for _, subject := range item.Subjects {
		subjects = append(subjects, Subject{
			Kind:      subject.Kind,
			Name:      subject.Name,
			Namespace: subject.Namespace,
		})
	}
	return BindingResource{
		ID:        string(item.UID),
		ClusterID: clusterID,
		Namespace: item.Namespace,
		Kind:      "RoleBinding",
		Name:      item.Name,
		UID:       string(item.UID),
		RoleRef:   RoleRef{Kind: item.RoleRef.Kind, Name: item.RoleRef.Name},
		Subjects:  subjects,
		Source:    "Kubernetes API",
		UpdatedAt: item.CreationTimestamp.Time,
	}
}

func mapKubernetesClusterBinding(clusterID string, item rbacv1.ClusterRoleBinding) BindingResource {
	subjects := make([]Subject, 0, len(item.Subjects))
	for _, subject := range item.Subjects {
		subjects = append(subjects, Subject{
			Kind:      subject.Kind,
			Name:      subject.Name,
			Namespace: subject.Namespace,
		})
	}
	return BindingResource{
		ID:        string(item.UID),
		ClusterID: clusterID,
		Kind:      "ClusterRoleBinding",
		Name:      item.Name,
		UID:       string(item.UID),
		RoleRef:   RoleRef{Kind: item.RoleRef.Kind, Name: item.RoleRef.Name},
		Subjects:  subjects,
		Source:    "Kubernetes API",
		UpdatedAt: item.CreationTimestamp.Time,
	}
}

func normalizeRoleResource(item RoleResource) RoleResource {
	item.ClusterID = strings.TrimSpace(item.ClusterID)
	item.Namespace = strings.TrimSpace(item.Namespace)
	item.Kind = strings.TrimSpace(item.Kind)
	item.Name = strings.TrimSpace(item.Name)
	return item
}

func normalizeBindingResource(item BindingResource) BindingResource {
	item.ClusterID = strings.TrimSpace(item.ClusterID)
	item.Namespace = strings.TrimSpace(item.Namespace)
	item.Kind = strings.TrimSpace(item.Kind)
	item.Name = strings.TrimSpace(item.Name)
	item.RoleRef.Kind = strings.TrimSpace(item.RoleRef.Kind)
	item.RoleRef.Name = strings.TrimSpace(item.RoleRef.Name)
	return item
}

func policyRulesFromRules(rules []Rule) []rbacv1.PolicyRule {
	out := make([]rbacv1.PolicyRule, 0, len(rules))
	for _, rule := range rules {
		out = append(out, rbacv1.PolicyRule{
			APIGroups: append([]string(nil), rule.APIGroups...),
			Resources: append([]string(nil), rule.Resources...),
			Verbs:     append([]string(nil), rule.Verbs...),
		})
	}
	return out
}

func subjectsFromModel(defaultNamespace string, subjects []Subject) []rbacv1.Subject {
	out := make([]rbacv1.Subject, 0, len(subjects))
	for _, subject := range subjects {
		namespace := strings.TrimSpace(subject.Namespace)
		if namespace == "" && strings.EqualFold(subject.Kind, "ServiceAccount") {
			namespace = defaultNamespace
		}
		out = append(out, rbacv1.Subject{
			Kind:      strings.TrimSpace(subject.Kind),
			Name:      strings.TrimSpace(subject.Name),
			Namespace: namespace,
		})
	}
	return out
}

func defaultRoleRefKind(kind string) string {
	kind = strings.TrimSpace(kind)
	if kind == "" {
		return "Role"
	}
	return kind
}
