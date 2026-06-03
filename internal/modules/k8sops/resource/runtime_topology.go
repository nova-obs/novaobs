package resource

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"novaobs/internal/modules/k8sops/kubeclient"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

type runtimeWorkloadSelector struct {
	Key      string
	Name     string
	Kind     string
	Selector map[string]string
}

type runtimeSecurityPolicyBinding struct {
	name                  string
	kind                  string
	summary               string
	createdAt             *string
	selector              map[string]string
	targetServices        []string
	targetWorkloadNames   []string
	targetServiceAccounts []string
	namespaceWide         bool
}

func (r KubernetesReader) ListRuntimeGroups(ctx context.Context, query RuntimeGroupsQuery) (RuntimeGroupsResponse, error) {
	query.ClusterID = strings.TrimSpace(query.ClusterID)
	query.Namespace = strings.TrimSpace(query.Namespace)
	if query.ClusterID == "" {
		return RuntimeGroupsResponse{}, ErrClusterRequired
	}
	if query.Namespace == "" || query.Namespace == "*" {
		return RuntimeGroupsResponse{}, ErrNamespaceRequired
	}
	if !r.allowed(ctx, query.ClusterID, query.Namespace) {
		return RuntimeGroupsResponse{}, ErrReadPermissionDenied
	}
	client, err := r.clients.Clientset(ctx, query.ClusterID)
	if err != nil {
		return RuntimeGroupsResponse{}, err
	}

	servicesList, err := client.CoreV1().Services(query.Namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return RuntimeGroupsResponse{}, err
	}
	deploymentsList, err := client.AppsV1().Deployments(query.Namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return RuntimeGroupsResponse{}, err
	}
	statefulSetsList, err := client.AppsV1().StatefulSets(query.Namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return RuntimeGroupsResponse{}, err
	}
	daemonSetsList, err := client.AppsV1().DaemonSets(query.Namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return RuntimeGroupsResponse{}, err
	}
	podsList, err := client.CoreV1().Pods(query.Namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return RuntimeGroupsResponse{}, err
	}

	services := make([]RuntimeServiceNode, 0, len(servicesList.Items))
	for _, item := range servicesList.Items {
		services = append(services, runtimeServiceNode(item))
	}
	sort.Slice(services, func(i, j int) bool { return services[i].Name < services[j].Name })

	workloads := make([]RuntimeWorkloadNode, 0, len(deploymentsList.Items)+len(statefulSetsList.Items)+len(daemonSetsList.Items))
	for _, item := range deploymentsList.Items {
		workloads = append(workloads, runtimeWorkloadFromDeployment(item))
	}
	for _, item := range statefulSetsList.Items {
		workloads = append(workloads, runtimeWorkloadFromStatefulSet(item))
	}
	for _, item := range daemonSetsList.Items {
		workloads = append(workloads, runtimeWorkloadFromDaemonSet(item))
	}
	sort.Slice(workloads, func(i, j int) bool {
		if workloads[i].Kind != workloads[j].Kind {
			return workloads[i].Kind < workloads[j].Kind
		}
		return workloads[i].Name < workloads[j].Name
	})

	workloadIndex := map[string]int{}
	selectors := make([]runtimeWorkloadSelector, 0, len(workloads))
	for index, workload := range workloads {
		workloadIndex[workload.Key] = index
		selectors = append(selectors, runtimeWorkloadSelector{
			Key:      workload.Key,
			Name:     workload.Name,
			Kind:     workload.Kind,
			Selector: workload.Selector,
		})
	}

	podSummaries := map[string]RuntimePodSummary{}
	serviceAccounts := map[string]map[string]struct{}{}
	configMaps := map[string]map[string]struct{}{}
	pvcs := map[string]map[string]struct{}{}
	for _, pod := range podsList.Items {
		workloadKey := bestRuntimeWorkloadMatch(selectors, pod.Labels)
		if workloadKey == "" {
			continue
		}
		summary := podSummaries[workloadKey]
		summary.Total++
		if len(pod.Spec.Containers) > 0 {
			summary.TotalContainers += uint64(len(pod.Spec.Containers))
		} else {
			summary.TotalContainers += uint64(len(pod.Status.ContainerStatuses))
		}
		for _, status := range pod.Status.ContainerStatuses {
			if status.Ready {
				summary.ReadyContainers++
			}
			summary.RestartCount += status.RestartCount
		}
		switch pod.Status.Phase {
		case corev1.PodRunning:
			summary.Running++
		case corev1.PodPending:
			summary.Pending++
		case corev1.PodFailed:
			summary.Failed++
		case corev1.PodSucceeded:
			summary.Succeeded++
		}
		podSummaries[workloadKey] = summary

		if serviceAccounts[workloadKey] == nil {
			serviceAccounts[workloadKey] = map[string]struct{}{}
		}
		account := strings.TrimSpace(pod.Spec.ServiceAccountName)
		if account == "" {
			account = "default"
		}
		serviceAccounts[workloadKey][account] = struct{}{}

		for _, name := range runtimeConfigMapRefs(pod.Spec) {
			if configMaps[workloadKey] == nil {
				configMaps[workloadKey] = map[string]struct{}{}
			}
			configMaps[workloadKey][name] = struct{}{}
		}
		for _, volume := range pod.Spec.Volumes {
			if volume.PersistentVolumeClaim == nil || strings.TrimSpace(volume.PersistentVolumeClaim.ClaimName) == "" {
				continue
			}
			if pvcs[workloadKey] == nil {
				pvcs[workloadKey] = map[string]struct{}{}
			}
			pvcs[workloadKey][volume.PersistentVolumeClaim.ClaimName] = struct{}{}
		}
	}

	for key, index := range workloadIndex {
		workloads[index].PodsSummary = podSummaries[key]
		workloads[index].ServiceAccounts = sortedRuntimeSet(serviceAccounts[key])
		workloads[index].ConfigMaps = sortedRuntimeSet(configMaps[key])
		workloads[index].PersistentVolumeClaims = sortedRuntimeSet(pvcs[key])
	}

	exposuresByService := map[string][]RuntimeExposureNode{}
	serviceToWorkloads, workloadToServices := runtimeServiceWorkloadLinks(services, workloads)
	if r.bundles != nil {
		services, workloads, exposuresByService = r.enrichRuntimeDynamicResources(ctx, query, services, workloads, workloadToServices)
	}

	groups := runtimeGroupsFromServicesAndWorkloads(services, workloads, exposuresByService, serviceToWorkloads, workloadToServices)
	return RuntimeGroupsResponse{
		ClusterID: query.ClusterID,
		Namespace: query.Namespace,
		Groups:    groups,
		Summary:   runtimeGroupsSummary(groups),
	}, nil
}

func (r KubernetesReader) enrichRuntimeDynamicResources(ctx context.Context, query RuntimeGroupsQuery, services []RuntimeServiceNode, workloads []RuntimeWorkloadNode, workloadToServices map[string][]string) ([]RuntimeServiceNode, []RuntimeWorkloadNode, map[string][]RuntimeExposureNode) {
	bundle, snapshot, err := r.bundleAndSnapshot(ctx, query.ClusterID)
	if err != nil {
		return services, workloads, map[string][]RuntimeExposureNode{}
	}
	hpas := listRuntimeObjects(ctx, bundle, snapshot, query.Namespace, "autoscaling/v2", "HorizontalPodAutoscaler")
	attachRuntimeHPAs(workloads, hpas)

	exposuresByService := map[string][]RuntimeExposureNode{}
	for _, exposure := range runtimeIngressExposures(listRuntimeObjects(ctx, bundle, snapshot, query.Namespace, "networking.k8s.io/v1", "Ingress"), query.Namespace) {
		for _, serviceRef := range exposure.ServiceRefs {
			exposuresByService[serviceRef] = append(exposuresByService[serviceRef], exposure)
		}
	}

	virtualServices := runtimeVirtualServiceExposures(listRuntimeObjects(ctx, bundle, snapshot, query.Namespace, "networking.istio.io/v1", "VirtualService"), query.Namespace)
	for _, exposure := range virtualServices {
		for _, serviceRef := range exposure.ServiceRefs {
			exposuresByService[serviceRef] = append(exposuresByService[serviceRef], exposure)
		}
	}

	gateways := runtimeGatewayExposures(listRuntimeObjects(ctx, bundle, snapshot, query.Namespace, "networking.istio.io/v1", "Gateway"))
	gatewaysByName := map[string]RuntimeExposureNode{}
	for _, gateway := range gateways {
		gatewaysByName[gateway.Name] = gateway
	}
	referencedGateways := map[string]struct{}{}
	for _, exposure := range virtualServices {
		for _, gateway := range exposure.Gateways {
			if normalized := normalizeRuntimeReferenceName(gateway); normalized != "" {
				referencedGateways[normalized] = struct{}{}
			}
		}
	}
	for gatewayName := range referencedGateways {
		gateway, ok := gatewaysByName[gatewayName]
		if !ok {
			continue
		}
		for _, serviceName := range collectRuntimeServicesForGateway(gateway.Name, exposuresByService) {
			exposuresByService[serviceName] = append(exposuresByService[serviceName], gateway)
		}
	}
	for serviceName := range exposuresByService {
		sort.Slice(exposuresByService[serviceName], func(i, j int) bool {
			left := exposuresByService[serviceName][i]
			right := exposuresByService[serviceName][j]
			if left.Kind != right.Kind {
				return left.Kind < right.Kind
			}
			return left.Name < right.Name
		})
		exposuresByService[serviceName] = dedupeRuntimeExposureNodes(exposuresByService[serviceName])
	}

	destinationRulesByService := map[string][]RuntimeDestinationRuleNode{}
	for _, rule := range runtimeDestinationRules(listRuntimeObjects(ctx, bundle, snapshot, query.Namespace, "networking.istio.io/v1", "DestinationRule"), query.Namespace) {
		serviceRef, ok := normalizeRuntimeServiceRef(rule.Host, query.Namespace)
		if !ok {
			continue
		}
		destinationRulesByService[serviceRef] = append(destinationRulesByService[serviceRef], rule)
	}
	for serviceName := range destinationRulesByService {
		sort.Slice(destinationRulesByService[serviceName], func(i, j int) bool {
			return destinationRulesByService[serviceName][i].Name < destinationRulesByService[serviceName][j].Name
		})
		destinationRulesByService[serviceName] = dedupeRuntimeDestinationRules(destinationRulesByService[serviceName])
	}

	for index := range services {
		service := &services[index]
		exposures := exposuresByService[service.Name]
		service.Hosts = uniqueSortedStrings(runtimeFlattenExposureHosts(exposures))
		service.VirtualServices = uniqueSortedStrings(runtimeExposureNamesByKind(exposures, "VirtualService"))
		service.Gateways = uniqueSortedStrings(runtimeGatewayNames(exposures))
		service.VirtualServiceDetails = buildRuntimeVirtualServiceDetails(exposures)
		service.DestinationRuleDetails = destinationRulesByService[service.Name]
		service.DestinationRules = runtimeDestinationRuleNames(service.DestinationRuleDetails)
		sortRuntimeServiceDetails(service)
	}
	securityBindings := runtimeSecurityBindings(ctx, bundle, snapshot, query.Namespace)
	attachRuntimeSecurityPolicies(workloads, securityBindings, workloadToServices)
	return services, workloads, exposuresByService
}

func listRuntimeObjects(ctx context.Context, bundle kubeclient.Bundle, snapshot kubeclient.CapabilitySnapshot, namespace string, apiVersion string, kind string) []unstructured.Unstructured {
	resolved, err := resolveResourceVersion(snapshot, apiVersion, kind)
	if err != nil {
		return []unstructured.Unstructured{}
	}
	list, err := dynamicResource(bundle, resolved, namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return []unstructured.Unstructured{}
	}
	return append([]unstructured.Unstructured(nil), list.Items...)
}

func runtimeServiceNode(item corev1.Service) RuntimeServiceNode {
	ports := make([]RuntimeServicePort, 0, len(item.Spec.Ports))
	for _, port := range item.Spec.Ports {
		ports = append(ports, RuntimeServicePort{
			Name:       stringPointerOrNil(port.Name),
			Port:       port.Port,
			TargetPort: port.TargetPort.String(),
			Protocol:   string(port.Protocol),
			NodePort:   int32PointerOrNil(port.NodePort),
		})
	}
	return RuntimeServiceNode{
		Name:        item.Name,
		ServiceType: string(item.Spec.Type),
		ClusterIP:   item.Spec.ClusterIP,
		Selectors:   copyLabels(item.Spec.Selector),
		Ports:       ports,
		CreatedAt:   runtimeCreatedAt(item.CreationTimestamp),
	}
}

func runtimeWorkloadFromDeployment(item appsv1.Deployment) RuntimeWorkloadNode {
	return RuntimeWorkloadNode{
		Key:            "Deployment/" + item.Name,
		Name:           item.Name,
		Kind:           "Deployment",
		Selector:       copyLabels(item.Spec.Selector.MatchLabels),
		TemplateLabels: copyLabels(item.Spec.Template.Labels),
		Replicas:       item.Spec.Replicas,
		ReadyReplicas:  &item.Status.ReadyReplicas,
		CreatedAt:      runtimeCreatedAt(item.CreationTimestamp),
	}
}

func runtimeWorkloadFromStatefulSet(item appsv1.StatefulSet) RuntimeWorkloadNode {
	return RuntimeWorkloadNode{
		Key:            "StatefulSet/" + item.Name,
		Name:           item.Name,
		Kind:           "StatefulSet",
		Selector:       copyLabels(item.Spec.Selector.MatchLabels),
		TemplateLabels: copyLabels(item.Spec.Template.Labels),
		Replicas:       item.Spec.Replicas,
		ReadyReplicas:  &item.Status.ReadyReplicas,
		CreatedAt:      runtimeCreatedAt(item.CreationTimestamp),
	}
}

func runtimeWorkloadFromDaemonSet(item appsv1.DaemonSet) RuntimeWorkloadNode {
	return RuntimeWorkloadNode{
		Key:                    "DaemonSet/" + item.Name,
		Name:                   item.Name,
		Kind:                   "DaemonSet",
		Selector:               copyLabels(item.Spec.Selector.MatchLabels),
		TemplateLabels:         copyLabels(item.Spec.Template.Labels),
		DesiredNumberScheduled: &item.Status.DesiredNumberScheduled,
		CurrentNumberScheduled: &item.Status.CurrentNumberScheduled,
		NumberReady:            &item.Status.NumberReady,
		CreatedAt:              runtimeCreatedAt(item.CreationTimestamp),
	}
}

type runtimeAppGroup struct {
	services  []string
	workloads []string
}

func runtimeGroupsFromServicesAndWorkloads(services []RuntimeServiceNode, workloads []RuntimeWorkloadNode, exposuresByService map[string][]RuntimeExposureNode, serviceToWorkloads map[string][]string, workloadToServices map[string][]string) []RuntimeGroup {
	servicesByName := map[string]RuntimeServiceNode{}
	for _, service := range services {
		servicesByName[service.Name] = service
	}
	workloadsByKey := map[string]RuntimeWorkloadNode{}
	for _, workload := range workloads {
		workloadsByKey[workload.Key] = workload
	}

	appGroups := runtimeGroupsByAppLabels(services, workloads)
	visitedServices := map[string]struct{}{}
	visitedWorkloads := map[string]struct{}{}
	groups := make([]RuntimeGroup, 0, len(services)+len(workloads))
	groupIndex := 1

	for _, appName := range sortedMapKeys(appGroups) {
		group := appGroups[appName]
		if len(group.services) == 0 && len(group.workloads) == 0 {
			continue
		}
		for _, name := range group.services {
			visitedServices[name] = struct{}{}
		}
		for _, key := range group.workloads {
			visitedWorkloads[key] = struct{}{}
		}
		groups = append(groups, buildRuntimeGroup(groupIndex, group.services, group.workloads, servicesByName, workloadsByKey, exposuresByService, &appName))
		groupIndex++
	}

	for _, serviceName := range sortedMapKeys(servicesByName) {
		if _, ok := visitedServices[serviceName]; ok {
			continue
		}
		componentServices, componentWorkloads := collectRuntimeComponent(serviceName, "", serviceToWorkloads, workloadToServices, visitedServices, visitedWorkloads)
		if len(componentServices) == 0 && len(componentWorkloads) == 0 {
			continue
		}
		groups = append(groups, buildRuntimeGroup(groupIndex, componentServices, componentWorkloads, servicesByName, workloadsByKey, exposuresByService, nil))
		groupIndex++
	}

	for _, workloadKey := range sortedMapKeys(workloadsByKey) {
		if _, ok := visitedWorkloads[workloadKey]; ok {
			continue
		}
		componentServices, componentWorkloads := collectRuntimeComponent("", workloadKey, serviceToWorkloads, workloadToServices, visitedServices, visitedWorkloads)
		if len(componentServices) == 0 && len(componentWorkloads) == 0 {
			continue
		}
		groups = append(groups, buildRuntimeGroup(groupIndex, componentServices, componentWorkloads, servicesByName, workloadsByKey, exposuresByService, nil))
		groupIndex++
	}

	sort.Slice(groups, func(i, j int) bool {
		if groups[i].Summary.PodsTotal != groups[j].Summary.PodsTotal {
			return groups[i].Summary.PodsTotal > groups[j].Summary.PodsTotal
		}
		if groups[i].Summary.WorkloadsTotal != groups[j].Summary.WorkloadsTotal {
			return groups[i].Summary.WorkloadsTotal > groups[j].Summary.WorkloadsTotal
		}
		return groups[i].DisplayName < groups[j].DisplayName
	})
	return groups
}

func runtimeGroupsByAppLabels(services []RuntimeServiceNode, workloads []RuntimeWorkloadNode) map[string]runtimeAppGroup {
	keys := []string{"app.kubernetes.io/name", "app.kubernetes.io/instance", "app", "k8s-app"}
	result := map[string]runtimeAppGroup{}
	for _, service := range services {
		for _, key := range keys {
			value := strings.TrimSpace(service.Selectors[key])
			if value == "" {
				continue
			}
			group := result[value]
			group.services = appendUniqueString(group.services, service.Name)
			result[value] = group
			break
		}
	}
	for _, workload := range workloads {
		for _, key := range keys {
			value := strings.TrimSpace(workload.TemplateLabels[key])
			if value == "" {
				continue
			}
			group := result[value]
			group.workloads = appendUniqueString(group.workloads, workload.Key)
			result[value] = group
			break
		}
	}
	for name, group := range result {
		sort.Strings(group.services)
		sort.Strings(group.workloads)
		result[name] = group
	}
	return result
}

func runtimeServiceWorkloadLinks(services []RuntimeServiceNode, workloads []RuntimeWorkloadNode) (map[string][]string, map[string][]string) {
	serviceToWorkloads := map[string][]string{}
	workloadToServices := map[string][]string{}
	for _, service := range services {
		if len(service.Selectors) == 0 {
			continue
		}
		for _, workload := range workloads {
			if runtimeSelectorMatches(service.Selectors, workload.TemplateLabels) {
				serviceToWorkloads[service.Name] = appendUniqueString(serviceToWorkloads[service.Name], workload.Key)
				workloadToServices[workload.Key] = appendUniqueString(workloadToServices[workload.Key], service.Name)
			}
		}
	}
	return serviceToWorkloads, workloadToServices
}

func collectRuntimeComponent(startService string, startWorkload string, serviceToWorkloads map[string][]string, workloadToServices map[string][]string, visitedServices map[string]struct{}, visitedWorkloads map[string]struct{}) ([]string, []string) {
	queueServices := []string{}
	queueWorkloads := []string{}
	if startService != "" {
		queueServices = append(queueServices, startService)
	}
	if startWorkload != "" {
		queueWorkloads = append(queueWorkloads, startWorkload)
	}

	componentServices := []string{}
	componentWorkloads := []string{}
	for len(queueServices) > 0 || len(queueWorkloads) > 0 {
		if len(queueServices) > 0 {
			serviceName := queueServices[0]
			queueServices = queueServices[1:]
			if _, ok := visitedServices[serviceName]; ok {
				continue
			}
			visitedServices[serviceName] = struct{}{}
			componentServices = append(componentServices, serviceName)
			for _, workloadKey := range serviceToWorkloads[serviceName] {
				if _, ok := visitedWorkloads[workloadKey]; !ok {
					queueWorkloads = append(queueWorkloads, workloadKey)
				}
			}
			continue
		}

		workloadKey := queueWorkloads[0]
		queueWorkloads = queueWorkloads[1:]
		if _, ok := visitedWorkloads[workloadKey]; ok {
			continue
		}
		visitedWorkloads[workloadKey] = struct{}{}
		componentWorkloads = append(componentWorkloads, workloadKey)
		for _, serviceName := range workloadToServices[workloadKey] {
			if _, ok := visitedServices[serviceName]; !ok {
				queueServices = append(queueServices, serviceName)
			}
		}
	}
	sort.Strings(componentServices)
	sort.Strings(componentWorkloads)
	return componentServices, componentWorkloads
}

func buildRuntimeGroup(index int, serviceNames []string, workloadKeys []string, servicesByName map[string]RuntimeServiceNode, workloadsByKey map[string]RuntimeWorkloadNode, exposuresByService map[string][]RuntimeExposureNode, appName *string) RuntimeGroup {
	services := make([]RuntimeServiceNode, 0, len(serviceNames))
	for _, name := range serviceNames {
		if service, ok := servicesByName[name]; ok {
			services = append(services, service)
		}
	}
	workloads := make([]RuntimeWorkloadNode, 0, len(workloadKeys))
	for _, key := range workloadKeys {
		if workload, ok := workloadsByKey[key]; ok {
			workloads = append(workloads, workload)
		}
	}
	sort.Slice(services, func(i, j int) bool { return services[i].Name < services[j].Name })
	sort.Slice(workloads, func(i, j int) bool {
		if workloads[i].Kind != workloads[j].Kind {
			return workloads[i].Kind < workloads[j].Kind
		}
		return workloads[i].Name < workloads[j].Name
	})

	exposures := []RuntimeExposureNode{}
	for _, serviceName := range serviceNames {
		exposures = append(exposures, exposuresByService[serviceName]...)
	}
	sort.Slice(exposures, func(i, j int) bool {
		if exposures[i].Kind != exposures[j].Kind {
			return exposures[i].Kind < exposures[j].Kind
		}
		return exposures[i].Name < exposures[j].Name
	})
	exposures = dedupeRuntimeExposureNodes(exposures)

	displayName := inferRuntimeGroupName(services, workloads)
	if appName != nil && strings.TrimSpace(*appName) != "" {
		displayName = *appName
	}
	key := fmt.Sprintf("runtime-group:%d", index)
	if len(services) > 0 {
		key = "service:" + services[0].Name
	} else if len(workloads) > 0 {
		key = "workload:" + workloads[0].Key
	}
	group := RuntimeGroup{
		Key:         key,
		DisplayName: displayName,
		IsVirtual:   len(services) == 0,
		Exposures:   exposures,
		Services:    services,
		Workloads:   workloads,
	}
	group.Summary = runtimeGroupSummary(group)
	return group
}

func inferRuntimeGroupName(services []RuntimeServiceNode, workloads []RuntimeWorkloadNode) string {
	labelKeys := []string{"app.kubernetes.io/name", "app.kubernetes.io/instance", "app", "k8s-app"}
	for _, service := range services {
		for _, key := range labelKeys {
			if value := strings.TrimSpace(service.Selectors[key]); value != "" {
				return value
			}
		}
	}
	for _, workload := range workloads {
		for _, key := range labelKeys {
			if value := strings.TrimSpace(workload.TemplateLabels[key]); value != "" {
				return value
			}
		}
	}
	if len(services) > 0 {
		return services[0].Name
	}
	if len(workloads) > 0 {
		return workloads[0].Name
	}
	return "runtime-group"
}

func attachRuntimeHPAs(workloads []RuntimeWorkloadNode, hpas []unstructured.Unstructured) {
	workloadIndex := map[string]int{}
	for index, workload := range workloads {
		workloadIndex[workload.Key] = index
	}
	for _, hpa := range hpas {
		targetKind, _, _ := unstructured.NestedString(hpa.Object, "spec", "scaleTargetRef", "kind")
		targetName, _, _ := unstructured.NestedString(hpa.Object, "spec", "scaleTargetRef", "name")
		if targetKind == "" || targetName == "" {
			continue
		}
		index, ok := workloadIndex[targetKind+"/"+targetName]
		if !ok {
			continue
		}
		workloads[index].HPAs = append(workloads[index].HPAs, RuntimeHPANode{
			Name:            hpa.GetName(),
			MinReplicas:     nestedInt32Pointer(hpa.Object, "spec", "minReplicas"),
			MaxReplicas:     nestedInt32Pointer(hpa.Object, "spec", "maxReplicas"),
			CurrentReplicas: nestedInt32Pointer(hpa.Object, "status", "currentReplicas"),
		})
		sort.Slice(workloads[index].HPAs, func(i, j int) bool { return workloads[index].HPAs[i].Name < workloads[index].HPAs[j].Name })
	}
}

func runtimeIngressExposures(items []unstructured.Unstructured, namespace string) []RuntimeExposureNode {
	exposures := make([]RuntimeExposureNode, 0, len(items))
	for _, item := range items {
		hosts := []string{}
		serviceRefs := []string{}
		rules, _, _ := unstructured.NestedSlice(item.Object, "spec", "rules")
		for _, raw := range rules {
			rule, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			if host, ok := rule["host"].(string); ok && strings.TrimSpace(host) != "" {
				hosts = appendUniqueSorted(hosts, host)
			}
			httpRule, ok := rule["http"].(map[string]any)
			if !ok {
				continue
			}
			paths, _ := httpRule["paths"].([]any)
			for _, rawPath := range paths {
				path, ok := rawPath.(map[string]any)
				if !ok {
					continue
				}
				serviceName, ok := ingressBackendServiceName(path["backend"], namespace)
				if ok {
					serviceRefs = appendUniqueSorted(serviceRefs, serviceName)
				}
			}
		}
		if defaultBackend, found, _ := unstructured.NestedMap(item.Object, "spec", "defaultBackend"); found {
			serviceName, ok := ingressBackendServiceName(defaultBackend, namespace)
			if ok {
				serviceRefs = appendUniqueSorted(serviceRefs, serviceName)
			}
		}
		exposures = append(exposures, RuntimeExposureNode{
			Key:         "Ingress/" + item.GetName(),
			Name:        item.GetName(),
			Kind:        "Ingress",
			Hosts:       hosts,
			ServiceRefs: serviceRefs,
			Gateways:    []string{},
			CreatedAt:   runtimeCreatedAt(item.GetCreationTimestamp()),
		})
	}
	return exposures
}

func runtimeVirtualServiceExposures(items []unstructured.Unstructured, namespace string) []RuntimeExposureNode {
	exposures := make([]RuntimeExposureNode, 0, len(items))
	for _, item := range items {
		hosts, _, _ := unstructured.NestedStringSlice(item.Object, "spec", "hosts")
		rawGateways, _, _ := unstructured.NestedStringSlice(item.Object, "spec", "gateways")
		gateways := []string{}
		for _, gateway := range rawGateways {
			if normalized := normalizeRuntimeReferenceName(gateway); normalized != "" {
				gateways = append(gateways, normalized)
			}
		}
		serviceRefs, targets, routes := runtimeVirtualServiceTraffic(item, namespace)
		exposures = append(exposures, RuntimeExposureNode{
			Key:          "VirtualService/" + item.GetName(),
			Name:         item.GetName(),
			Kind:         "VirtualService",
			Hosts:        uniqueSortedStrings(hosts),
			Gateways:     uniqueSortedStrings(gateways),
			ServiceRefs:  serviceRefs,
			RouteTargets: targets,
			RouteRules:   routes,
			CreatedAt:    runtimeCreatedAt(item.GetCreationTimestamp()),
		})
	}
	sort.Slice(exposures, func(i, j int) bool { return exposures[i].Name < exposures[j].Name })
	return exposures
}

func runtimeGatewayExposures(items []unstructured.Unstructured) []RuntimeExposureNode {
	exposures := make([]RuntimeExposureNode, 0, len(items))
	for _, item := range items {
		hosts := []string{}
		if servers, found, _ := unstructured.NestedSlice(item.Object, "spec", "servers"); found {
			for _, raw := range servers {
				server, ok := raw.(map[string]any)
				if ok {
					hosts = append(hosts, stringSliceFromMap(server, "hosts")...)
				}
			}
		}
		exposures = append(exposures, RuntimeExposureNode{
			Key:          "Gateway/" + item.GetName(),
			Name:         item.GetName(),
			Kind:         "Gateway",
			Hosts:        uniqueSortedStrings(hosts),
			Gateways:     []string{},
			ServiceRefs:  []string{},
			RouteTargets: []RuntimeRouteTarget{},
			RouteRules:   []RuntimeVirtualServiceRouteNode{},
			CreatedAt:    runtimeCreatedAt(item.GetCreationTimestamp()),
		})
	}
	sort.Slice(exposures, func(i, j int) bool { return exposures[i].Name < exposures[j].Name })
	return exposures
}

func runtimeVirtualServiceTraffic(item unstructured.Unstructured, namespace string) ([]string, []RuntimeRouteTarget, []RuntimeVirtualServiceRouteNode) {
	serviceRefs := map[string]struct{}{}
	rules := []RuntimeVirtualServiceRouteNode{}
	for _, protocol := range []string{"http", "tcp", "tls"} {
		values, found, _ := unstructured.NestedSlice(item.Object, "spec", protocol)
		if !found {
			continue
		}
		for _, raw := range values {
			routeMap, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			nodeTargets := runtimeRouteTargets(routeMap["route"], namespace)
			if len(nodeTargets) == 0 {
				continue
			}
			for _, target := range nodeTargets {
				if serviceRef, ok := normalizeRuntimeServiceRef(target.Host, namespace); ok {
					serviceRefs[serviceRef] = struct{}{}
				}
			}
			matches := runtimeVirtualServiceRouteMatches(routeMap, strings.ToUpper(protocol))
			if len(matches) == 0 {
				matches = []RuntimeVirtualServiceMatchNode{defaultRuntimeVirtualServiceMatch()}
			}
			rules = append(rules, RuntimeVirtualServiceRouteNode{
				Name:       stringPointerFromMap(routeMap, "name"),
				Protocol:   strings.ToUpper(protocol),
				RewriteURI: stringPointerFromNestedMap(routeMap, "rewrite", "uri"),
				Matches:    matches,
				Targets:    nodeTargets,
			})
		}
	}
	sort.Slice(rules, func(i, j int) bool {
		if rules[i].Protocol != rules[j].Protocol {
			return rules[i].Protocol < rules[j].Protocol
		}
		if stringPointerValue(rules[i].Name) != stringPointerValue(rules[j].Name) {
			return stringPointerValue(rules[i].Name) < stringPointerValue(rules[j].Name)
		}
		return runtimeRouteMatchSummary(rules[i].Matches) < runtimeRouteMatchSummary(rules[j].Matches)
	})
	targets := []RuntimeRouteTarget{}
	for _, rule := range rules {
		targets = append(targets, rule.Targets...)
	}
	sortRuntimeRouteTargets(targets)
	return sortedRuntimeSet(serviceRefs), dedupeRuntimeRouteTargets(targets), rules
}

func runtimeVirtualServiceRouteMatches(routeMap map[string]any, protocol string) []RuntimeVirtualServiceMatchNode {
	values := []map[string]any{}
	switch raw := routeMap["match"].(type) {
	case []any:
		for _, item := range raw {
			if match, ok := item.(map[string]any); ok {
				values = append(values, match)
			}
		}
	case map[string]any:
		values = append(values, raw)
	}
	if len(values) == 0 {
		if raw, ok := routeMap["Match"].(map[string]any); ok {
			values = append(values, raw)
		}
	}
	result := make([]RuntimeVirtualServiceMatchNode, 0, len(values))
	for _, value := range values {
		match, ok := buildRuntimeVirtualServiceMatch(value, protocol)
		if ok {
			result = append(result, match)
		}
	}
	return result
}

func defaultRuntimeVirtualServiceMatch() RuntimeVirtualServiceMatchNode {
	return RuntimeVirtualServiceMatchNode{
		Summary:       "Default match",
		Headers:       []RuntimeHeaderMatchNode{},
		Gateways:      []string{},
		SourceLabels:  []string{},
		SourceSubnets: []string{},
		SNIHosts:      []string{},
	}
}

func buildRuntimeVirtualServiceMatch(item map[string]any, protocol string) (RuntimeVirtualServiceMatchNode, bool) {
	uri := runtimeStringMatch(item, "uri")
	scheme := runtimeStringMatch(item, "scheme")
	method := runtimeStringMatch(item, "method")
	authority := runtimeStringMatch(item, "authority")
	port := stringPointerFromNumericOrString(item, "port")
	sourceNamespace := stringPointerFromMap(item, "sourceNamespace")
	gateways := []string{}
	for _, gateway := range stringSliceFromMap(item, "gateways") {
		if normalized := normalizeRuntimeReferenceName(gateway); normalized != "" {
			gateways = append(gateways, normalized)
		}
	}
	gateways = uniqueSortedStrings(gateways)
	headers := []RuntimeHeaderMatchNode{}
	if rawHeaders, ok := item["headers"].(map[string]any); ok {
		for key, value := range rawHeaders {
			matcher := runtimeStringMatchAny(value)
			if matcher != nil {
				headers = append(headers, RuntimeHeaderMatchNode{Name: key, Matcher: *matcher})
			}
		}
		sort.Slice(headers, func(i, j int) bool { return headers[i].Name < headers[j].Name })
	}
	sourceLabels := []string{}
	if rawLabels, ok := item["sourceLabels"].(map[string]any); ok {
		for key, value := range rawLabels {
			text, ok := value.(string)
			if ok && strings.TrimSpace(text) != "" {
				sourceLabels = append(sourceLabels, key+"="+text)
			}
		}
		sort.Strings(sourceLabels)
	}
	sourceSubnets := uniqueSortedStrings(stringSliceFromMap(item, "sourceSubnets"))
	sniHosts := []string{}
	if protocol == "TLS" {
		sniHosts = uniqueSortedStrings(stringSliceFromMap(item, "sniHosts"))
	}
	parts := []string{}
	for _, matcher := range []struct {
		label string
		value *RuntimeStringMatchNode
	}{
		{label: "uri", value: uri},
		{label: "scheme", value: scheme},
		{label: "method", value: method},
		{label: "authority", value: authority},
	} {
		if matcher.value != nil {
			parts = append(parts, matcher.label+" "+matcher.value.MatchType+" "+matcher.value.Value)
		}
	}
	if port != nil {
		parts = append(parts, "port "+*port)
	}
	if sourceNamespace != nil {
		parts = append(parts, "ns "+*sourceNamespace)
	}
	if len(gateways) > 0 {
		parts = append(parts, "gw "+strings.Join(gateways, ", "))
	}
	if len(headers) > 0 {
		headerParts := make([]string, 0, len(headers))
		for _, header := range headers {
			headerParts = append(headerParts, header.Name+" "+header.Matcher.MatchType+" "+header.Matcher.Value)
		}
		parts = append(parts, "headers "+strings.Join(headerParts, "; "))
	}
	if len(sourceLabels) > 0 {
		parts = append(parts, "labels "+strings.Join(sourceLabels, ", "))
	}
	if len(sniHosts) > 0 {
		parts = append(parts, "sni "+strings.Join(sniHosts, ", "))
	}
	if len(sourceSubnets) > 0 {
		parts = append(parts, "src "+strings.Join(sourceSubnets, ", "))
	}
	if len(parts) == 0 {
		return RuntimeVirtualServiceMatchNode{}, false
	}
	return RuntimeVirtualServiceMatchNode{
		Summary:         strings.Join(parts, " · "),
		URI:             uri,
		Scheme:          scheme,
		Method:          method,
		Authority:       authority,
		Headers:         headers,
		Gateways:        gateways,
		SourceLabels:    sourceLabels,
		SourceNamespace: sourceNamespace,
		SourceSubnets:   sourceSubnets,
		Port:            port,
		SNIHosts:        sniHosts,
	}, true
}

func runtimeRouteTargets(raw any, namespace string) []RuntimeRouteTarget {
	values, ok := raw.([]any)
	if !ok {
		return []RuntimeRouteTarget{}
	}
	result := make([]RuntimeRouteTarget, 0, len(values))
	for _, rawRoute := range values {
		route, ok := rawRoute.(map[string]any)
		if !ok {
			continue
		}
		destination, ok := route["destination"].(map[string]any)
		if !ok {
			continue
		}
		host, _ := destination["host"].(string)
		if strings.TrimSpace(host) == "" {
			continue
		}
		result = append(result, RuntimeRouteTarget{
			Host:   normalizeRuntimeTargetHost(host, namespace),
			Subset: stringPointerFromMap(destination, "subset"),
			Port:   runtimeDestinationPort(destination["port"]),
			Weight: int32PointerFromAny(route["weight"]),
		})
	}
	return result
}

func runtimeDestinationRules(items []unstructured.Unstructured, namespace string) []RuntimeDestinationRuleNode {
	rules := make([]RuntimeDestinationRuleNode, 0, len(items))
	for _, item := range items {
		host, _, _ := unstructured.NestedString(item.Object, "spec", "host")
		subsets := []string{}
		subsetDetails := []RuntimeDestinationRuleSubsetNode{}
		values, _, _ := unstructured.NestedSlice(item.Object, "spec", "subsets")
		for _, raw := range values {
			value, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			name, _ := value["name"].(string)
			if strings.TrimSpace(name) == "" {
				continue
			}
			subsets = appendUniqueSorted(subsets, name)
			subsetDetails = append(subsetDetails, RuntimeDestinationRuleSubsetNode{Name: name, Labels: stringMapFromAny(value["labels"])})
		}
		sort.Slice(subsetDetails, func(i, j int) bool { return subsetDetails[i].Name < subsetDetails[j].Name })
		_, hasTrafficPolicy, _ := unstructured.NestedMap(item.Object, "spec", "trafficPolicy")
		exportTo, _, _ := unstructured.NestedStringSlice(item.Object, "spec", "exportTo")
		rules = append(rules, RuntimeDestinationRuleNode{
			Name:             item.GetName(),
			Host:             host,
			Subsets:          uniqueSortedStrings(subsets),
			SubsetDetails:    dedupeRuntimeDestinationRuleSubsets(subsetDetails),
			HasTrafficPolicy: hasTrafficPolicy,
			ExportTo:         uniqueSortedStrings(exportTo),
			CreatedAt:        runtimeCreatedAt(item.GetCreationTimestamp()),
		})
		_ = namespace
	}
	sort.Slice(rules, func(i, j int) bool { return rules[i].Name < rules[j].Name })
	return rules
}

func runtimeSecurityBindings(ctx context.Context, bundle kubeclient.Bundle, snapshot kubeclient.CapabilitySnapshot, namespace string) []runtimeSecurityPolicyBinding {
	bindings := []runtimeSecurityPolicyBinding{}
	bindings = append(bindings, peerAuthenticationBindings(listRuntimeObjects(ctx, bundle, snapshot, namespace, "security.istio.io/v1", "PeerAuthentication"))...)
	bindings = append(bindings, authorizationPolicyBindings(listRuntimeObjects(ctx, bundle, snapshot, namespace, "security.istio.io/v1", "AuthorizationPolicy"))...)
	bindings = append(bindings, requestAuthenticationBindings(listRuntimeObjects(ctx, bundle, snapshot, namespace, "security.istio.io/v1", "RequestAuthentication"))...)
	bindings = append(bindings, legacyPolicyBindings(listRuntimeObjects(ctx, bundle, snapshot, namespace, "authentication.istio.io/v1alpha1", "Policy"))...)
	bindings = append(bindings, legacyMeshPolicyBindings(listRuntimeObjects(ctx, bundle, snapshot, namespace, "authentication.istio.io/v1alpha1", "MeshPolicy"))...)
	bindings = append(bindings, legacyServiceRoleBindingBindings(listRuntimeObjects(ctx, bundle, snapshot, namespace, "rbac.istio.io/v1alpha1", "ServiceRoleBinding"), namespace)...)
	bindings = append(bindings, legacyClusterRbacConfigBindings(listRuntimeObjects(ctx, bundle, snapshot, namespace, "rbac.istio.io/v1alpha1", "ClusterRbacConfig"))...)
	sort.Slice(bindings, func(i, j int) bool {
		if bindings[i].kind != bindings[j].kind {
			return bindings[i].kind < bindings[j].kind
		}
		return bindings[i].name < bindings[j].name
	})
	return dedupeRuntimeSecurityBindings(bindings)
}

func peerAuthenticationBindings(items []unstructured.Unstructured) []runtimeSecurityPolicyBinding {
	result := make([]runtimeSecurityPolicyBinding, 0, len(items))
	for _, item := range items {
		selector, _, _ := unstructured.NestedStringMap(item.Object, "spec", "selector", "matchLabels")
		mode, _, _ := unstructured.NestedString(item.Object, "spec", "mtls", "mode")
		if mode == "" {
			mode = "UNSET"
		}
		portLevel, found, _ := unstructured.NestedMap(item.Object, "spec", "portLevelMtls")
		summary := "mTLS " + mode
		if found && len(portLevel) > 0 {
			summary = fmt.Sprintf("mTLS %s · %d port-level policies", mode, len(portLevel))
		}
		result = append(result, runtimeSecurityPolicyBinding{
			name:          item.GetName(),
			kind:          "PeerAuthentication",
			summary:       summary,
			createdAt:     runtimeCreatedAt(item.GetCreationTimestamp()),
			selector:      selector,
			namespaceWide: len(selector) == 0,
		})
	}
	return result
}

func authorizationPolicyBindings(items []unstructured.Unstructured) []runtimeSecurityPolicyBinding {
	result := make([]runtimeSecurityPolicyBinding, 0, len(items))
	for _, item := range items {
		selector, _, _ := unstructured.NestedStringMap(item.Object, "spec", "selector", "matchLabels")
		action, _, _ := unstructured.NestedString(item.Object, "spec", "action")
		if action == "" {
			action = "ALLOW"
		}
		rules, found, _ := unstructured.NestedSlice(item.Object, "spec", "rules")
		ruleCount := 0
		if found {
			ruleCount = len(rules)
		}
		result = append(result, runtimeSecurityPolicyBinding{
			name:          item.GetName(),
			kind:          "AuthorizationPolicy",
			summary:       action + " · " + strconv.Itoa(ruleCount) + " rules",
			createdAt:     runtimeCreatedAt(item.GetCreationTimestamp()),
			selector:      selector,
			namespaceWide: len(selector) == 0,
		})
	}
	return result
}

func requestAuthenticationBindings(items []unstructured.Unstructured) []runtimeSecurityPolicyBinding {
	result := make([]runtimeSecurityPolicyBinding, 0, len(items))
	for _, item := range items {
		selector, _, _ := unstructured.NestedStringMap(item.Object, "spec", "selector", "matchLabels")
		jwtRules, found, _ := unstructured.NestedSlice(item.Object, "spec", "jwtRules")
		ruleCount := 0
		if found {
			ruleCount = len(jwtRules)
		}
		result = append(result, runtimeSecurityPolicyBinding{
			name:          item.GetName(),
			kind:          "RequestAuthentication",
			summary:       strconv.Itoa(ruleCount) + " JWT rules",
			createdAt:     runtimeCreatedAt(item.GetCreationTimestamp()),
			selector:      selector,
			namespaceWide: len(selector) == 0,
		})
	}
	return result
}

func legacyPolicyBindings(items []unstructured.Unstructured) []runtimeSecurityPolicyBinding {
	result := make([]runtimeSecurityPolicyBinding, 0, len(items))
	for _, item := range items {
		targets := extractRuntimeNamedTargets(item.Object, "spec", "targets")
		peers, hasPeers, _ := unstructured.NestedSlice(item.Object, "spec", "peers")
		origins, hasOrigins, _ := unstructured.NestedSlice(item.Object, "spec", "origins")
		principalBinding, _, _ := unstructured.NestedString(item.Object, "spec", "principalBinding")
		summaryParts := []string{}
		if hasPeers && len(peers) > 0 {
			summaryParts = append(summaryParts, "peer")
		}
		if hasOrigins && len(origins) > 0 {
			summaryParts = append(summaryParts, "origin")
		}
		if strings.TrimSpace(principalBinding) != "" {
			summaryParts = append(summaryParts, principalBinding)
		}
		summary := "Legacy auth"
		if len(summaryParts) > 0 {
			summary += " · " + strings.Join(summaryParts, " · ")
		}
		result = append(result, runtimeSecurityPolicyBinding{
			name:                item.GetName(),
			kind:                "Policy",
			summary:             summary,
			createdAt:           runtimeCreatedAt(item.GetCreationTimestamp()),
			selector:            map[string]string{},
			targetServices:      targets,
			targetWorkloadNames: targets,
			namespaceWide:       len(targets) == 0,
		})
	}
	return result
}

func legacyMeshPolicyBindings(items []unstructured.Unstructured) []runtimeSecurityPolicyBinding {
	result := make([]runtimeSecurityPolicyBinding, 0, len(items))
	for _, item := range items {
		mode := "UNSET"
		peers, found, _ := unstructured.NestedSlice(item.Object, "spec", "peers")
		if found && len(peers) > 0 {
			if peer, ok := peers[0].(map[string]any); ok {
				if mtls, ok := peer["mtls"].(map[string]any); ok {
					if value, ok := mtls["mode"].(string); ok && strings.TrimSpace(value) != "" {
						mode = strings.TrimSpace(value)
					}
				}
			}
		}
		result = append(result, runtimeSecurityPolicyBinding{
			name:          item.GetName(),
			kind:          "MeshPolicy",
			summary:       "Mesh mTLS " + mode,
			createdAt:     runtimeCreatedAt(item.GetCreationTimestamp()),
			selector:      map[string]string{},
			namespaceWide: true,
		})
	}
	return result
}

func legacyServiceRoleBindingBindings(items []unstructured.Unstructured, namespace string) []runtimeSecurityPolicyBinding {
	result := make([]runtimeSecurityPolicyBinding, 0, len(items))
	for _, item := range items {
		serviceAccounts := map[string]struct{}{}
		subjects, found, _ := unstructured.NestedSlice(item.Object, "spec", "subjects")
		if found {
			for _, rawSubject := range subjects {
				subject, ok := rawSubject.(map[string]any)
				if !ok {
					continue
				}
				if user, ok := subject["user"].(string); ok {
					if serviceAccount, matched := extractRuntimeServiceAccountFromSubject(user, namespace); matched {
						serviceAccounts[serviceAccount] = struct{}{}
					}
				}
				if properties, ok := subject["properties"].(map[string]any); ok {
					if principal, ok := properties["source.principal"].(string); ok {
						if serviceAccount, matched := extractRuntimeServiceAccountFromSubject(principal, namespace); matched {
							serviceAccounts[serviceAccount] = struct{}{}
						}
					}
				}
			}
		}
		roleRef, _, _ := unstructured.NestedString(item.Object, "spec", "roleRef", "name")
		if strings.TrimSpace(roleRef) == "" {
			roleRef = "-"
		}
		targetServiceAccounts := sortedRuntimeSet(serviceAccounts)
		result = append(result, runtimeSecurityPolicyBinding{
			name:                  item.GetName(),
			kind:                  "ServiceRoleBinding",
			summary:               fmt.Sprintf("Role %s · Subject %d", roleRef, len(targetServiceAccounts)),
			createdAt:             runtimeCreatedAt(item.GetCreationTimestamp()),
			selector:              map[string]string{},
			targetServiceAccounts: targetServiceAccounts,
			namespaceWide:         len(targetServiceAccounts) == 0,
		})
	}
	return result
}

func legacyClusterRbacConfigBindings(items []unstructured.Unstructured) []runtimeSecurityPolicyBinding {
	result := make([]runtimeSecurityPolicyBinding, 0, len(items))
	for _, item := range items {
		mode, _, _ := unstructured.NestedString(item.Object, "spec", "mode")
		if mode == "" {
			mode = "ON"
		}
		result = append(result, runtimeSecurityPolicyBinding{
			name:          item.GetName(),
			kind:          "ClusterRbacConfig",
			summary:       "Mode " + mode,
			createdAt:     runtimeCreatedAt(item.GetCreationTimestamp()),
			selector:      map[string]string{},
			namespaceWide: true,
		})
	}
	return result
}

func attachRuntimeSecurityPolicies(workloads []RuntimeWorkloadNode, bindings []runtimeSecurityPolicyBinding, workloadToServices map[string][]string) {
	for index := range workloads {
		policies := []RuntimeSecurityPolicyNode{}
		linkedServices := map[string]struct{}{}
		for _, serviceName := range workloadToServices[workloads[index].Key] {
			linkedServices[serviceName] = struct{}{}
		}
		for _, binding := range bindings {
			if !runtimeSecurityPolicyAppliesToWorkload(binding, workloads[index], linkedServices) {
				continue
			}
			policies = append(policies, RuntimeSecurityPolicyNode{
				Name:      binding.name,
				Kind:      binding.kind,
				Summary:   binding.summary,
				CreatedAt: binding.createdAt,
			})
		}
		sort.Slice(policies, func(i, j int) bool {
			if policies[i].Kind != policies[j].Kind {
				return policies[i].Kind < policies[j].Kind
			}
			return policies[i].Name < policies[j].Name
		})
		workloads[index].SecurityPolicies = dedupeRuntimeSecurityPolicies(policies)
	}
}

func runtimeSecurityPolicyAppliesToWorkload(binding runtimeSecurityPolicyBinding, workload RuntimeWorkloadNode, linkedServices map[string]struct{}) bool {
	if binding.namespaceWide {
		return true
	}
	if len(binding.selector) > 0 && runtimeSelectorMatches(binding.selector, workload.TemplateLabels) {
		return true
	}
	for _, target := range binding.targetWorkloadNames {
		if target == workload.Name {
			return true
		}
	}
	for _, target := range binding.targetServices {
		if _, ok := linkedServices[target]; ok {
			return true
		}
	}
	for _, target := range binding.targetServiceAccounts {
		for _, serviceAccount := range workload.ServiceAccounts {
			if target == serviceAccount {
				return true
			}
		}
	}
	return false
}

func extractRuntimeNamedTargets(object map[string]any, fields ...string) []string {
	values, found, _ := unstructured.NestedSlice(object, fields...)
	if !found {
		return []string{}
	}
	result := map[string]struct{}{}
	for _, raw := range values {
		target, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if name, ok := target["name"].(string); ok && strings.TrimSpace(name) != "" {
			result[strings.TrimSpace(name)] = struct{}{}
		}
	}
	return sortedRuntimeSet(result)
}

func extractRuntimeServiceAccountFromSubject(value string, namespace string) (string, bool) {
	text := strings.TrimSpace(value)
	if text == "" {
		return "", false
	}
	prefixes := []string{
		"cluster.local/ns/" + namespace + "/sa/",
		"spiffe://cluster.local/ns/" + namespace + "/sa/",
		"system:serviceaccount:" + namespace + ":",
	}
	for _, prefix := range prefixes {
		if strings.HasPrefix(text, prefix) {
			name := strings.TrimSpace(strings.TrimPrefix(text, prefix))
			return name, name != ""
		}
	}
	if !strings.Contains(text, "/") && !strings.Contains(text, ":") && !strings.Contains(text, ".") {
		return text, true
	}
	return "", false
}

func dedupeRuntimeSecurityBindings(values []runtimeSecurityPolicyBinding) []runtimeSecurityPolicyBinding {
	seen := map[string]struct{}{}
	result := make([]runtimeSecurityPolicyBinding, 0, len(values))
	for _, value := range values {
		key := value.kind + "/" + value.name
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, value)
	}
	return result
}

func dedupeRuntimeSecurityPolicies(values []RuntimeSecurityPolicyNode) []RuntimeSecurityPolicyNode {
	seen := map[string]struct{}{}
	result := make([]RuntimeSecurityPolicyNode, 0, len(values))
	for _, value := range values {
		key := value.Kind + "/" + value.Name
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, value)
	}
	return result
}

func runtimeGroupsSummary(groups []RuntimeGroup) RuntimeGroupsSummary {
	summary := RuntimeGroupsSummary{GroupCount: uint64(len(groups))}
	for _, group := range groups {
		summary.ServiceCount += uint64(len(group.Services))
		summary.WorkloadCount += uint64(len(group.Workloads))
		summary.VirtualServiceCount += group.Summary.VirtualServicesTotal
		summary.GatewayCount += group.Summary.GatewaysTotal
		summary.DestinationRuleCount += group.Summary.DestinationRulesTotal
		summary.SecurityPolicyCount += group.Summary.SecurityPoliciesTotal
		for _, workload := range group.Workloads {
			summary.PodCount += workload.PodsSummary.Total
			summary.PVCCount += uint64(len(workload.PersistentVolumeClaims))
		}
	}
	return summary
}

func runtimeGroupSummary(group RuntimeGroup) RuntimeGroupSummary {
	summary := RuntimeGroupSummary{
		ServicesTotal:         uint64(len(group.Services)),
		WorkloadsTotal:        uint64(len(group.Workloads)),
		GatewaysTotal:         uint64(len(runtimeGroupGateways(group))),
		DestinationRulesTotal: uint64(len(runtimeGroupDestinationRules(group))),
	}
	for _, exposure := range group.Exposures {
		if exposure.Kind == "VirtualService" {
			summary.VirtualServicesTotal++
		}
	}
	for _, workload := range group.Workloads {
		summary.PodsTotal += workload.PodsSummary.Total
		summary.RunningPods += workload.PodsSummary.Running
		summary.PendingPods += workload.PodsSummary.Pending
		summary.FailedPods += workload.PodsSummary.Failed
		summary.RestartCount += workload.PodsSummary.RestartCount
		summary.PersistentVolumeClaimsTotal += uint64(len(workload.PersistentVolumeClaims))
		summary.SecurityPoliciesTotal += uint64(len(workload.SecurityPolicies))
	}
	return summary
}

func runtimeGroupGateways(group RuntimeGroup) []string {
	set := map[string]struct{}{}
	for _, service := range group.Services {
		for _, item := range service.Gateways {
			set[item] = struct{}{}
		}
	}
	for _, exposure := range group.Exposures {
		for _, item := range exposure.Gateways {
			set[item] = struct{}{}
		}
	}
	return sortedRuntimeSet(set)
}

func runtimeGroupDestinationRules(group RuntimeGroup) []string {
	set := map[string]struct{}{}
	for _, service := range group.Services {
		for _, item := range service.DestinationRules {
			set[item] = struct{}{}
		}
	}
	return sortedRuntimeSet(set)
}

func bestRuntimeWorkloadMatch(selectors []runtimeWorkloadSelector, labels map[string]string) string {
	bestKey := ""
	bestSize := -1
	for _, selector := range selectors {
		if !runtimeSelectorMatches(selector.Selector, labels) {
			continue
		}
		if len(selector.Selector) > bestSize {
			bestKey = selector.Key
			bestSize = len(selector.Selector)
		}
	}
	return bestKey
}

func runtimeSelectorMatches(selector map[string]string, labels map[string]string) bool {
	if len(selector) == 0 {
		return false
	}
	for key, value := range selector {
		if labels[key] != value {
			return false
		}
	}
	return true
}

func appendUniqueString(values []string, candidate string) []string {
	for _, value := range values {
		if value == candidate {
			return values
		}
	}
	return append(values, candidate)
}

func sortedMapKeys[T any](values map[string]T) []string {
	result := make([]string, 0, len(values))
	for key := range values {
		result = append(result, key)
	}
	sort.Strings(result)
	return result
}

func dedupeRuntimeExposureNodes(values []RuntimeExposureNode) []RuntimeExposureNode {
	result := make([]RuntimeExposureNode, 0, len(values))
	seen := map[string]struct{}{}
	for _, value := range values {
		if _, ok := seen[value.Key]; ok {
			continue
		}
		seen[value.Key] = struct{}{}
		result = append(result, value)
	}
	return result
}

func dedupeRuntimeDestinationRules(values []RuntimeDestinationRuleNode) []RuntimeDestinationRuleNode {
	result := make([]RuntimeDestinationRuleNode, 0, len(values))
	seen := map[string]struct{}{}
	for _, value := range values {
		if _, ok := seen[value.Name]; ok {
			continue
		}
		seen[value.Name] = struct{}{}
		result = append(result, value)
	}
	return result
}

func dedupeRuntimeDestinationRuleSubsets(values []RuntimeDestinationRuleSubsetNode) []RuntimeDestinationRuleSubsetNode {
	result := make([]RuntimeDestinationRuleSubsetNode, 0, len(values))
	seen := map[string]struct{}{}
	for _, value := range values {
		if _, ok := seen[value.Name]; ok {
			continue
		}
		seen[value.Name] = struct{}{}
		result = append(result, value)
	}
	return result
}

func dedupeRuntimeVirtualServiceDetails(values []RuntimeVirtualServiceNode) []RuntimeVirtualServiceNode {
	result := make([]RuntimeVirtualServiceNode, 0, len(values))
	seen := map[string]struct{}{}
	for _, value := range values {
		if _, ok := seen[value.Name]; ok {
			continue
		}
		seen[value.Name] = struct{}{}
		result = append(result, value)
	}
	return result
}

func dedupeRuntimeRouteTargets(values []RuntimeRouteTarget) []RuntimeRouteTarget {
	result := make([]RuntimeRouteTarget, 0, len(values))
	seen := map[string]struct{}{}
	for _, value := range values {
		key := value.Host + "|" + stringPointerValue(value.Subset) + "|" + stringPointerValue(value.Port) + "|" + int32PointerValue(value.Weight)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, value)
	}
	return result
}

func sortRuntimeRouteTargets(values []RuntimeRouteTarget) {
	sort.Slice(values, func(i, j int) bool {
		if values[i].Host != values[j].Host {
			return values[i].Host < values[j].Host
		}
		if stringPointerValue(values[i].Subset) != stringPointerValue(values[j].Subset) {
			return stringPointerValue(values[i].Subset) < stringPointerValue(values[j].Subset)
		}
		if stringPointerValue(values[i].Port) != stringPointerValue(values[j].Port) {
			return stringPointerValue(values[i].Port) < stringPointerValue(values[j].Port)
		}
		return int32PointerValue(values[i].Weight) < int32PointerValue(values[j].Weight)
	})
}

func runtimeConfigMapRefs(spec corev1.PodSpec) []string {
	set := map[string]struct{}{}
	for _, volume := range spec.Volumes {
		if volume.ConfigMap != nil && strings.TrimSpace(volume.ConfigMap.Name) != "" {
			set[volume.ConfigMap.Name] = struct{}{}
		}
	}
	for _, container := range append(append([]corev1.Container{}, spec.InitContainers...), spec.Containers...) {
		for _, envFrom := range container.EnvFrom {
			if envFrom.ConfigMapRef != nil && strings.TrimSpace(envFrom.ConfigMapRef.Name) != "" {
				set[envFrom.ConfigMapRef.Name] = struct{}{}
			}
		}
		for _, env := range container.Env {
			if env.ValueFrom != nil && env.ValueFrom.ConfigMapKeyRef != nil && strings.TrimSpace(env.ValueFrom.ConfigMapKeyRef.Name) != "" {
				set[env.ValueFrom.ConfigMapKeyRef.Name] = struct{}{}
			}
		}
	}
	return sortedRuntimeSet(set)
}

func normalizeRuntimeTargetHost(host string, namespace string) string {
	if serviceRef, ok := normalizeRuntimeServiceRef(host, namespace); ok {
		return serviceRef
	}
	return host
}

func normalizeRuntimeServiceRef(value string, namespace string) (string, bool) {
	text := strings.TrimSpace(strings.ToLower(value))
	if text == "" || text == "mesh" || text == "*" {
		return "", false
	}
	if strings.Contains(text, "/") {
		parts := strings.SplitN(text, "/", 2)
		if len(parts) == 2 && strings.TrimSpace(parts[1]) != "" {
			text = strings.TrimSpace(parts[1])
		}
	}
	parts := strings.Split(text, ".")
	if len(parts) == 1 {
		return parts[0], true
	}
	if len(parts) >= 2 && parts[1] == namespace {
		return parts[0], true
	}
	if len(parts) >= 2 && parts[1] == "svc" {
		return parts[0], true
	}
	if len(parts) >= 3 && parts[2] == "svc" && (parts[1] == namespace || namespace == "") {
		return parts[0], true
	}
	return parts[0], true
}

func normalizeRuntimeReferenceName(value string) string {
	text := strings.TrimSpace(value)
	if text == "" {
		return ""
	}
	if strings.Contains(text, "/") {
		parts := strings.SplitN(text, "/", 2)
		return strings.TrimSpace(parts[1])
	}
	return text
}

func collectRuntimeServicesForGateway(gatewayName string, exposuresByService map[string][]RuntimeExposureNode) []string {
	result := []string{}
	for serviceName, exposures := range exposuresByService {
		for _, exposure := range exposures {
			if exposure.Kind != "VirtualService" {
				continue
			}
			for _, gateway := range exposure.Gateways {
				if normalizeRuntimeReferenceName(gateway) == gatewayName {
					result = append(result, serviceName)
					goto nextService
				}
			}
		}
	nextService:
	}
	sort.Strings(result)
	return result
}

func buildRuntimeVirtualServiceDetails(exposures []RuntimeExposureNode) []RuntimeVirtualServiceNode {
	result := []RuntimeVirtualServiceNode{}
	for _, exposure := range exposures {
		if exposure.Kind != "VirtualService" {
			continue
		}
		result = append(result, RuntimeVirtualServiceNode{
			Name:         exposure.Name,
			Hosts:        append([]string{}, exposure.Hosts...),
			Gateways:     append([]string{}, exposure.Gateways...),
			RouteTargets: append([]RuntimeRouteTarget{}, exposure.RouteTargets...),
			Routes:       append([]RuntimeVirtualServiceRouteNode{}, exposure.RouteRules...),
			CreatedAt:    exposure.CreatedAt,
		})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Name < result[j].Name })
	return dedupeRuntimeVirtualServiceDetails(result)
}

func runtimeDestinationRuleNames(values []RuntimeDestinationRuleNode) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		result = append(result, value.Name)
	}
	return result
}

func runtimeFlattenExposureHosts(exposures []RuntimeExposureNode) []string {
	result := []string{}
	for _, exposure := range exposures {
		result = append(result, exposure.Hosts...)
	}
	return result
}

func runtimeGatewayNames(exposures []RuntimeExposureNode) []string {
	result := []string{}
	for _, exposure := range exposures {
		if exposure.Kind == "Gateway" {
			result = append(result, exposure.Name)
		}
		result = append(result, exposure.Gateways...)
	}
	return result
}

func runtimeExposureNamesByKind(exposures []RuntimeExposureNode, kind string) []string {
	result := []string{}
	for _, exposure := range exposures {
		if exposure.Kind == kind {
			result = append(result, exposure.Name)
		}
	}
	return result
}

func runtimeStringMatch(value map[string]any, field string) *RuntimeStringMatchNode {
	var target any = value
	if field != "" {
		if raw, ok := value[field].(map[string]any); ok {
			target = raw
		} else if raw, ok := value[field].(string); ok {
			trimmed := strings.TrimSpace(raw)
			if trimmed == "" {
				return nil
			}
			return &RuntimeStringMatchNode{MatchType: "exact", Value: trimmed}
		} else {
			title := strings.ToUpper(field[:1]) + field[1:]
			if raw, ok := value[title].(map[string]any); ok {
				target = raw
			} else if raw, ok := value[title].(string); ok {
				trimmed := strings.TrimSpace(raw)
				if trimmed == "" {
					return nil
				}
				return &RuntimeStringMatchNode{MatchType: "exact", Value: trimmed}
			} else {
				return nil
			}
		}
	}
	if raw, ok := target.(string); ok {
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" {
			return nil
		}
		return &RuntimeStringMatchNode{MatchType: "exact", Value: trimmed}
	}
	targetMap, ok := target.(map[string]any)
	if !ok {
		return nil
	}
	for _, key := range []string{"exact", "prefix", "regex"} {
		if raw, ok := targetMap[key].(string); ok && strings.TrimSpace(raw) != "" {
			return &RuntimeStringMatchNode{MatchType: key, Value: strings.TrimSpace(raw)}
		}
		title := strings.ToUpper(key[:1]) + key[1:]
		if raw, ok := targetMap[title].(string); ok && strings.TrimSpace(raw) != "" {
			return &RuntimeStringMatchNode{MatchType: key, Value: strings.TrimSpace(raw)}
		}
	}
	return nil
}

func runtimeStringMatchAny(value any) *RuntimeStringMatchNode {
	switch raw := value.(type) {
	case string:
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" {
			return nil
		}
		return &RuntimeStringMatchNode{MatchType: "exact", Value: trimmed}
	case map[string]any:
		return runtimeStringMatch(raw, "")
	default:
		return nil
	}
}

func runtimeRouteMatchSummary(values []RuntimeVirtualServiceMatchNode) string {
	parts := make([]string, 0, len(values))
	for _, value := range values {
		parts = append(parts, value.Summary)
	}
	return strings.Join(parts, "|")
}

func ingressBackendServiceName(raw any, namespace string) (string, bool) {
	backend, ok := raw.(map[string]any)
	if !ok {
		return "", false
	}
	if service, ok := backend["service"].(map[string]any); ok {
		if name, ok := service["name"].(string); ok {
			return normalizeRuntimeServiceRef(name, namespace)
		}
	}
	if name, ok := backend["serviceName"].(string); ok {
		return normalizeRuntimeServiceRef(name, namespace)
	}
	return "", false
}

func runtimeServiceNameFromHost(host string, serviceNames map[string]struct{}) string {
	host = strings.TrimSpace(host)
	if host == "" {
		return ""
	}
	if _, ok := serviceNames[host]; ok {
		return host
	}
	name := strings.Split(host, ".")[0]
	if _, ok := serviceNames[name]; ok {
		return name
	}
	return ""
}

func runtimeDestinationPort(raw any) *string {
	value, ok := raw.(map[string]any)
	if !ok {
		return nil
	}
	if number, ok := value["number"]; ok {
		text := strings.TrimSpace(numberToString(number))
		if text != "" {
			return &text
		}
	}
	if name, ok := value["name"].(string); ok && strings.TrimSpace(name) != "" {
		name = strings.TrimSpace(name)
		return &name
	}
	return nil
}

func nestedInt32Pointer(object map[string]any, fields ...string) *int32 {
	value, found, _ := unstructured.NestedInt64(object, fields...)
	if !found {
		return nil
	}
	out := int32(value)
	return &out
}

func int32PointerFromAny(raw any) *int32 {
	switch value := raw.(type) {
	case int64:
		out := int32(value)
		return &out
	case int32:
		out := value
		return &out
	case int:
		out := int32(value)
		return &out
	case float64:
		out := int32(value)
		return &out
	default:
		return nil
	}
}

func stringPointerFromMap(values map[string]any, key string) *string {
	value, ok := values[key].(string)
	if !ok || strings.TrimSpace(value) == "" {
		return nil
	}
	value = strings.TrimSpace(value)
	return &value
}

func stringPointerFromNestedMap(values map[string]any, mapKey string, key string) *string {
	nested, ok := values[mapKey].(map[string]any)
	if !ok {
		return nil
	}
	return stringPointerFromMap(nested, key)
}

func stringPointerFromNumericOrString(values map[string]any, key string) *string {
	if raw, ok := values[key]; ok {
		text := strings.TrimSpace(numberToString(raw))
		if text != "" {
			return &text
		}
	}
	return nil
}

func stringPointerValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func int32PointerValue(value *int32) string {
	if value == nil {
		return ""
	}
	return strconv.Itoa(int(*value))
}

func stringSliceFromMap(values map[string]any, key string) []string {
	raw, ok := values[key]
	if !ok {
		return []string{}
	}
	switch items := raw.(type) {
	case []string:
		return append([]string{}, items...)
	case []any:
		result := make([]string, 0, len(items))
		for _, item := range items {
			if text, ok := item.(string); ok && strings.TrimSpace(text) != "" {
				result = append(result, text)
			}
		}
		return result
	case string:
		if strings.TrimSpace(items) == "" {
			return []string{}
		}
		return []string{items}
	default:
		return []string{}
	}
}

func stringMapFromAny(raw any) map[string]string {
	values, ok := raw.(map[string]any)
	if !ok {
		return map[string]string{}
	}
	result := map[string]string{}
	for key, value := range values {
		if text, ok := value.(string); ok {
			result[key] = text
		}
	}
	return result
}

func appendUniqueSorted(values []string, next ...string) []string {
	set := map[string]struct{}{}
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			set[value] = struct{}{}
		}
	}
	for _, value := range next {
		if strings.TrimSpace(value) != "" {
			set[value] = struct{}{}
		}
	}
	return sortedRuntimeSet(set)
}

func uniqueSortedStrings(values []string) []string {
	return appendUniqueSorted(nil, values...)
}

func sortRuntimeServiceDetails(service *RuntimeServiceNode) {
	sort.Slice(service.VirtualServiceDetails, func(i, j int) bool {
		return service.VirtualServiceDetails[i].Name < service.VirtualServiceDetails[j].Name
	})
	sort.Slice(service.DestinationRuleDetails, func(i, j int) bool {
		return service.DestinationRuleDetails[i].Name < service.DestinationRuleDetails[j].Name
	})
}

func numberToString(raw any) string {
	switch value := raw.(type) {
	case int:
		return strconv.Itoa(value)
	case int32:
		return strconv.Itoa(int(value))
	case int64:
		return strconv.FormatInt(value, 10)
	case float64:
		return strconv.Itoa(int(value))
	case string:
		return value
	default:
		return ""
	}
}

func sortedRuntimeSet(set map[string]struct{}) []string {
	if len(set) == 0 {
		return []string{}
	}
	values := make([]string, 0, len(set))
	for value := range set {
		values = append(values, value)
	}
	sort.Strings(values)
	return values
}

func runtimeCreatedAt(value metav1.Time) *string {
	if value.Time.IsZero() {
		return nil
	}
	text := value.Time.Format(time.RFC3339)
	return &text
}

func stringPointerOrNil(value string) *string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return &value
}

func int32PointerOrNil(value int32) *int32 {
	if value == 0 {
		return nil
	}
	return &value
}
