# K8s 运维功能等价矩阵

本矩阵用于约束 `startorch` / `u8s-front` 能力迁移到 NovaObs 的第一版范围。每个能力必须有明确的来源、NovaObs API、RBAC 动作和验证方式；没有进入 golden contract 的能力，不能在最终验收时声明“功能不破坏”。

| 能力 ID | Startorch service | Startorch route | u8s-front 页面 | NovaObs API | NovaObs 模块 | RBAC | 审计 | Golden fixture | 迁移状态 |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| dashboard.stats | dashboard.GetDashboardStats | GET /api/dashboard/stats | Dashboard.jsx | GET /api/v1/k8s/dashboard | k8sops/dashboard | k8s.dashboard:read:cluster | 否 | 是 | 未开始 |
| cluster.list | cluster.ListClusters | GET /api/cluster | ClusterManagement.jsx | GET /api/v1/k8s/clusters | k8sops/cluster | k8s.cluster:read:global | 否 | 是 | 未开始 |
| cluster.create | cluster.CreateCluster | POST /api/cluster | ClusterManagement.jsx | POST /api/v1/k8s/clusters | k8sops/cluster | k8s.cluster:create:global | 是 | 是 | 未开始 |
| cluster.update | cluster.UpdateCluster | PUT /api/cluster | ClusterManagement.jsx | PATCH /api/v1/k8s/clusters/:cluster_id | k8sops/cluster | k8s.cluster:update:cluster | 是 | 是 | 未开始 |
| cluster.delete | cluster.DeleteCluster | DELETE /api/cluster | ClusterManagement.jsx | DELETE /api/v1/k8s/clusters/:cluster_id | k8sops/cluster | k8s.cluster:delete:cluster | 是 | 是 | 未开始 |
| cluster.connectivity-check | cluster.InspectKubeconfig | POST /api/cluster/test | ClusterManagement.jsx | POST /api/v1/k8s/clusters/connectivity-check | k8sops/cluster | k8s.cluster:preview:global | 是 | 是 | 未开始 |
| namespace.list | namespace.ListNamespaces | GET /api/namespace | NamespaceManagement.jsx | GET /api/v1/k8s/namespaces | k8sops/namespace | k8s.namespace:read:cluster | 否 | 是 | 未开始 |
| namespace.create | namespace.CreateNamespace | POST /api/namespace | NamespaceManagement.jsx | POST /api/v1/k8s/namespaces | k8sops/namespace | k8s.namespace:create:cluster | 是 | 是 | 未开始 |
| namespace.delete | namespace.DeleteNamespace | DELETE /api/namespace | NamespaceManagement.jsx | DELETE /api/v1/k8s/namespaces/:name | k8sops/namespace | k8s.namespace:delete:namespace | 是 | 是 | 未开始 |
| resource.list | k8sresource.ListK8sResources | GET /api/k8s/resources | ResourceManagement.jsx | GET /api/v1/k8s/resources | k8sops/resource | k8s.resource:read:namespace | 否 | 是 | 未开始 |
| resource.yaml | k8sresource.GetK8sResourceYAML | GET /api/k8s/resources/yaml | NamespaceResourceDetail.jsx | GET /api/v1/k8s/resources/yaml | k8sops/resource | k8s.resource:read:namespace | 否 | 是 | 未开始 |
| resource.delete | k8sresource.DeleteK8sResource | DELETE /api/k8s/resources | ResourceManagement.jsx | DELETE /api/v1/k8s/resources | k8sops/resource | k8s.resource:delete:namespace | 是 | 是 | 未开始 |
| pod.logs | k8sresource.GetPodLogs | GET /api/k8s/pod/logs | NamespaceResourceDetail.jsx | GET /api/v1/k8s/pod-logs | k8sops/resource | k8s.resource:read:namespace | 否 | 是 | 未开始 |
| runtime-groups.list | k8sresource.ListRuntimeGroups | GET /api/k8s/runtime-groups | ResourceManagement.jsx | GET /api/v1/k8s/runtime-groups | k8sops/resource | k8s.resource:read:namespace | 否 | 是 | 未开始 |
| serviceaccount.list | serviceaccount.ListServiceAccounts | GET /api/sa | ServiceAccountManagement.jsx | GET /api/v1/k8s/service-accounts | k8sops/serviceaccount | k8s.service-account:read:namespace | 否 | 是 | 未开始 |
| serviceaccount.create | serviceaccount.CreateServiceAccount | POST /api/sa | ServiceAccountManagement.jsx | POST /api/v1/k8s/service-accounts | k8sops/serviceaccount | k8s.service-account:create:namespace | 是 | 是 | 未开始 |
| serviceaccount.delete | serviceaccount.DeleteServiceAccount | DELETE /api/sa | ServiceAccountManagement.jsx | DELETE /api/v1/k8s/service-accounts | k8sops/serviceaccount | k8s.service-account:delete:namespace | 是 | 是 | 未开始 |
| rbac.role.list | role.ListRoles | GET /api/role | RoleManagement.jsx | GET /api/v1/k8s/rbac/roles | k8sops/rbac | k8s.rbac:read:namespace | 否 | 是 | 未开始 |
| rbac.role.create | role.CreateRole | POST /api/role | RoleManagement.jsx | POST /api/v1/k8s/rbac/roles | k8sops/rbac | k8s.rbac:create:namespace | 是 | 是 | 未开始 |
| rbac.role.update | role.UpdateRole | PUT /api/role | RoleManagement.jsx | PUT /api/v1/k8s/rbac/roles | k8sops/rbac | k8s.rbac:update:namespace | 是 | 是 | 未开始 |
| rbac.rolebinding.list | rolebinding.ListRoleBindings | GET /api/role_binding | RoleBindingManagement.jsx | GET /api/v1/k8s/rbac/role-bindings | k8sops/rbac | k8s.rbac:read:namespace | 否 | 是 | 未开始 |
| rbac.rolebinding.create | rolebinding.CreateRoleBinding | POST /api/role_binding | RoleBindingManagement.jsx | POST /api/v1/k8s/rbac/role-bindings | k8sops/rbac | k8s.rbac:create:namespace | 是 | 是 | 未开始 |
| rbac.rolebinding.delete | rolebinding.DeleteRoleBinding | DELETE /api/role_binding | RoleBindingManagement.jsx | DELETE /api/v1/k8s/rbac/role-bindings | k8sops/rbac | k8s.rbac:delete:namespace | 是 | 是 | 未开始 |
| rbac.rolebinding.bind | rolebinding.BindServiceAccount | POST /api/role_binding/bind | RoleBindingManagement.jsx | POST /api/v1/k8s/rbac/role-bindings/bind | k8sops/rbac | k8s.rbac:update:namespace | 是 | 是 | 未开始 |
| rbac.rolebinding.unbind | rolebinding.UnbindServiceAccount | POST /api/role_binding/unbind | RoleBindingManagement.jsx | POST /api/v1/k8s/rbac/role-bindings/unbind | k8sops/rbac | k8s.rbac:update:namespace | 是 | 是 | 未开始 |
| rbac.clusterrole.list | clusterrole.ListClusterRoles | GET /api/cluster_role | ClusterRoleManagement.jsx | GET /api/v1/k8s/rbac/cluster-roles | k8sops/rbac | k8s.rbac:read:cluster | 否 | 是 | 未开始 |
| rbac.clusterrole.create | clusterrole.CreateClusterRole | POST /api/cluster_role | ClusterRoleManagement.jsx | POST /api/v1/k8s/rbac/cluster-roles | k8sops/rbac | k8s.rbac:create:cluster | 是 | 是 | 未开始 |
| rbac.clusterrole.update | clusterrole.UpdateClusterRole | PUT /api/cluster_role | ClusterRoleManagement.jsx | PUT /api/v1/k8s/rbac/cluster-roles | k8sops/rbac | k8s.rbac:update:cluster | 是 | 是 | 未开始 |
| rbac.clusterrole.delete | clusterrole.DeleteClusterRole | DELETE /api/cluster_role | ClusterRoleManagement.jsx | DELETE /api/v1/k8s/rbac/cluster-roles | k8sops/rbac | k8s.rbac:delete:cluster | 是 | 是 | 未开始 |
| rbac.clusterrolebinding.list | clusterrolebinding.ListClusterRoleBindings | GET /api/cluster_role_binding | ClusterRoleBindingManagement.jsx | GET /api/v1/k8s/rbac/cluster-role-bindings | k8sops/rbac | k8s.rbac:read:cluster | 否 | 是 | 未开始 |
| rbac.clusterrolebinding.create | clusterrolebinding.CreateClusterRoleBinding | POST /api/cluster_role_binding | ClusterRoleBindingManagement.jsx | POST /api/v1/k8s/rbac/cluster-role-bindings | k8sops/rbac | k8s.rbac:create:cluster | 是 | 是 | 未开始 |
| rbac.clusterrolebinding.delete | clusterrolebinding.DeleteClusterRoleBinding | DELETE /api/cluster_role_binding | ClusterRoleBindingManagement.jsx | DELETE /api/v1/k8s/rbac/cluster-role-bindings | k8sops/rbac | k8s.rbac:delete:cluster | 是 | 是 | 未开始 |
| rbac.clusterrolebinding.bind | clusterrolebinding.BindServiceAccount | POST /api/cluster_role_binding/bind | ClusterRoleBindingManagement.jsx | POST /api/v1/k8s/rbac/cluster-role-bindings/bind | k8sops/rbac | k8s.rbac:update:cluster | 是 | 是 | 未开始 |
| rbac.clusterrolebinding.unbind | clusterrolebinding.UnbindServiceAccount | POST /api/cluster_role_binding/unbind | ClusterRoleBindingManagement.jsx | POST /api/v1/k8s/rbac/cluster-role-bindings/unbind | k8sops/rbac | k8s.rbac:update:cluster | 是 | 是 | 未开始 |
| kubeconfig.generate | kubecfg.GenerateKubeConfig | POST /api/kubeconfig | KubeconfigGenerator.jsx | POST /api/v1/k8s/kubeconfigs | k8sops/kubeconfig | k8s.kubeconfig:export:namespace | 是 | 是 | 未开始 |
| template.list | template.ListTemplates | GET /api/template/list | TemplateManagement.jsx | GET /api/v1/k8s/templates | k8sops/template | k8s.template:read:global | 否 | 是 | 未开始 |
| template.create | template.CreateTemplate | POST /api/template | TemplateManagement.jsx | POST /api/v1/k8s/templates | k8sops/template | k8s.template:create:global | 是 | 是 | 未开始 |
| template.get | template.GetTemplate | GET /api/template | TemplateManagement.jsx | GET /api/v1/k8s/templates/:template_id | k8sops/template | k8s.template:read:global | 否 | 是 | 未开始 |
| template.update | template.UpdateTemplate | PUT /api/template | TemplateManagement.jsx | PATCH /api/v1/k8s/templates/:template_id | k8sops/template | k8s.template:update:global | 是 | 是 | 未开始 |
| template.delete | template.DeleteTemplate | DELETE /api/template | TemplateManagement.jsx | DELETE /api/v1/k8s/templates/:template_id | k8sops/template | k8s.template:delete:global | 是 | 是 | 未开始 |
| template.render | template.RenderTemplate | POST /api/template/render | DeployManagement.jsx | POST /api/v1/k8s/templates/render | k8sops/template | k8s.template:preview:global | 是 | 是 | 未开始 |
| template.base | template.GetBaseTemplate | GET /api/template/base | TemplateManagement.jsx | GET /api/v1/k8s/templates/base | k8sops/template | k8s.template:read:global | 否 | 是 | 未开始 |
| deployment.apply | deploy.DeployResource | POST /api/deploy | DeployManagement.jsx | POST /api/v1/k8s/deployments | k8sops/deployment | k8s.deployment:deploy:namespace | 是 | 是 | 未开始 |
| deployment.delete | deploy.DeleteResource | DELETE /api/deploy | DeployManagement.jsx | DELETE /api/v1/k8s/deployments | k8sops/deployment | k8s.deployment:delete:namespace | 是 | 是 | 未开始 |
| deployment.history | deploy.ListDeployHistory | GET /api/deploy/history | DeployHistory.jsx | GET /api/v1/k8s/deployment-history | k8sops/deployment | k8s.deployment:read:namespace | 否 | 是 | 未开始 |
| deployment.history-detail | deploy.GetDeployHistoryDetail | GET /api/deploy/history/detail | DeployHistory.jsx | GET /api/v1/k8s/deployment-history/detail | k8sops/deployment | k8s.deployment:read:namespace | 否 | 是 | 未开始 |
| deployment.audit | deploy.ListDeployAudit | GET /api/deploy/audit | AuditCenter.jsx | GET /api/v1/k8s/audit-events | k8sops/deployment | k8s.audit:read:namespace | 否 | 是 | 未开始 |
| live-namespace.list | k8sresource.ListK8sNamespaces | GET /api/k8s/namespaces | ResourceManagement.jsx | GET /api/v1/k8s/live-namespaces | k8sops/resource | k8s.namespace:read:cluster | 否 | 是 | 未开始 |
| certificate.list | cert.ListCertificates | GET /api/certificates | CertificateManagement.jsx | GET /api/v1/k8s/certificates | k8sops/certificate | k8s.certificate:read:cluster | 否 | 是 | 未开始 |
| terminal.exec | kubectl.ExecCommand | POST /api/kubectl/exec | KubectlTerminal.jsx | POST /api/v1/k8s/terminal/exec | k8sops/terminal | k8s.terminal:exec:namespace | 是 | 是 | 未开始 |

## 明确废弃

| Startorch 能力 | 处理方式 | 原因 |
| --- | --- | --- |
| auth.Login / JWT | 废弃 | NovaObs 统一认证接管 |
| user.CreateUser / UpdateUser / DeleteUser | 不迁移为 K8s 独立用户体系 | K8s 模块中的用户管理仅作为 NovaObs RBAC subject/binding 入口 |
| menu_permissions / element_permissions | 废弃 | 前端只展示后端返回的 NovaObs capabilities，后端强制授权 |
