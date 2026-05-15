package collectorconfig

import (
	"context"
	"fmt"
	"strings"
	"time"

	"novaobs/internal/collectormanagement"
	"novaobs/internal/database"
	"novaobs/internal/servicecatalog"
	"novaobs/pkg/apperr"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

type Service struct {
	templates         database.CollectorPlatformTemplateStore
	overrides         database.CollectorGroupOverrideStore
	enrichmentPatches database.ServiceEnrichmentPatchStore
	parserRules       database.ServiceParserRuleStore
	pipelinePatches   database.ServicePipelinePatchStore
	collectorService  collectormanagement.Service
	serviceRepo       servicecatalog.Repository
}

func NewService(
	templates database.CollectorPlatformTemplateStore,
	overrides database.CollectorGroupOverrideStore,
	enrichmentPatches database.ServiceEnrichmentPatchStore,
	parserRules database.ServiceParserRuleStore,
	pipelinePatches database.ServicePipelinePatchStore,
	collectorService collectormanagement.Service,
	serviceRepo servicecatalog.Repository,
) Service {
	return Service{
		templates:         templates,
		overrides:         overrides,
		enrichmentPatches: enrichmentPatches,
		parserRules:       parserRules,
		pipelinePatches:   pipelinePatches,
		collectorService:  collectorService,
		serviceRepo:       serviceRepo,
	}
}

type ImportTemplateRequest struct {
	Name             string `json:"name"`
	Description      string `json:"description"`
	SourceAgentUID   string `json:"source_agent_uid"`
	BaseYAML         string `json:"base_yaml"`
	CollectorGroupID string `json:"collector_group_id"`
}

func (s Service) ImportTemplate(ctx context.Context, request ImportTemplateRequest) (CollectorPlatformTemplate, error) {
	baseYAML := strings.TrimSpace(request.BaseYAML)
	if baseYAML == "" {
		return CollectorPlatformTemplate{}, apperr.InvalidRequest("base_yaml 不能为空")
	}
	if _, _, _, err := RenderSources(ConfigSources{PlatformTemplate: &CollectorPlatformTemplate{BaseYAML: baseYAML}}); err != nil {
		return CollectorPlatformTemplate{}, apperr.InvalidRequest(fmt.Sprintf("base_yaml 无效: %v", err))
	}
	now := time.Now().UTC()
	template := CollectorPlatformTemplate{
		ID:             primitive.NewObjectID().Hex(),
		Name:           firstNonEmpty(request.Name, "imported-"+request.SourceAgentUID),
		Description:    request.Description,
		Source:         "agent_effective_config",
		SourceAgentUID: request.SourceAgentUID,
		BaseYAML:       baseYAML,
		ConfigHash:     HashYAML(baseYAML),
		Status:         "active",
		Version:        1,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	if err := s.templates.Insert(ctx, template); err != nil {
		return CollectorPlatformTemplate{}, err
	}
	if strings.TrimSpace(request.CollectorGroupID) != "" {
		_, err := s.collectorService.UpdateGroup(ctx, request.CollectorGroupID, collectormanagement.UpdateGroupRequest{PlatformTemplateID: &template.ID})
		if err != nil {
			return CollectorPlatformTemplate{}, err
		}
	}
	return template, nil
}

func (s Service) ListTemplates(ctx context.Context) ([]CollectorPlatformTemplate, error) {
	var templates []CollectorPlatformTemplate
	if err := s.templates.FindAll(ctx, &templates); err != nil {
		return nil, err
	}
	return templates, nil
}

func (s Service) GetTemplate(ctx context.Context, id string) (CollectorPlatformTemplate, error) {
	var template CollectorPlatformTemplate
	err := s.templates.FindByID(ctx, id, &template)
	return template, err
}

func (s Service) UpdateTemplate(ctx context.Context, id string, patch CollectorPlatformTemplate) (CollectorPlatformTemplate, error) {
	template, err := s.GetTemplate(ctx, id)
	if err != nil {
		return CollectorPlatformTemplate{}, err
	}
	if strings.TrimSpace(patch.Name) != "" {
		template.Name = strings.TrimSpace(patch.Name)
	}
	template.Description = patch.Description
	if strings.TrimSpace(patch.BaseYAML) != "" {
		if _, _, _, err := RenderSources(ConfigSources{PlatformTemplate: &CollectorPlatformTemplate{BaseYAML: strings.TrimSpace(patch.BaseYAML)}}); err != nil {
			return CollectorPlatformTemplate{}, apperr.InvalidRequest(fmt.Sprintf("base_yaml 无效: %v", err))
		}
		template.BaseYAML = strings.TrimSpace(patch.BaseYAML)
		template.ConfigHash = HashYAML(template.BaseYAML)
		template.Version++
	}
	template.UpdatedAt = time.Now().UTC()
	if err := s.templates.Update(ctx, id, template); err != nil {
		return CollectorPlatformTemplate{}, err
	}
	return template, nil
}

func (s Service) UpsertGroupOverride(ctx context.Context, groupID string, yaml string) (CollectorGroupOverride, error) {
	if _, err := s.collectorService.GetGroup(ctx, groupID); err != nil {
		return CollectorGroupOverride{}, err
	}
	override := CollectorGroupOverride{
		ID:               primitive.NewObjectID().Hex(),
		CollectorGroupID: groupID,
		OverrideYAML:     strings.TrimSpace(yaml),
		UpdatedAt:        time.Now().UTC(),
	}
	if err := s.overrides.Upsert(ctx, groupID, override); err != nil {
		return CollectorGroupOverride{}, err
	}
	return override, nil
}

func (s Service) BindServiceToCollectorGroup(ctx context.Context, serviceID string, collectorGroupID string) (ServiceEnrichmentPatch, error) {
	service, err := s.serviceRepo.Get(ctx, serviceID)
	if err != nil {
		return ServiceEnrichmentPatch{}, err
	}
	if strings.TrimSpace(collectorGroupID) == "" {
		return ServiceEnrichmentPatch{}, apperr.InvalidRequest("collector_group_id 不能为空")
	}
	if _, err := s.collectorService.GetGroup(ctx, collectorGroupID); err != nil {
		return ServiceEnrichmentPatch{}, err
	}
	patch := BuildEnrichmentPatch(serviceAttributes(service), collectorGroupID)
	now := time.Now().UTC()
	patch.ID = primitive.NewObjectID().Hex()
	patch.CreatedAt = now
	patch.UpdatedAt = now
	if err := s.enrichmentPatches.Upsert(ctx, serviceID, patch); err != nil {
		return ServiceEnrichmentPatch{}, err
	}
	return patch, nil
}

func (s Service) RegenerateEnrichmentPatch(ctx context.Context, serviceID string) (ServiceEnrichmentPatch, error) {
	service, err := s.serviceRepo.Get(ctx, serviceID)
	if err != nil {
		return ServiceEnrichmentPatch{}, err
	}
	collectorGroupID := ""
	var existing ServiceEnrichmentPatch
	if err := s.enrichmentPatches.FindByService(ctx, serviceID, &existing); err == nil {
		collectorGroupID = existing.CollectorGroupID
	}
	if strings.TrimSpace(collectorGroupID) == "" {
		collectorGroupID = serviceID
	}
	patch := BuildEnrichmentPatch(serviceAttributes(service), collectorGroupID)
	now := time.Now().UTC()
	patch.ID = primitive.NewObjectID().Hex()
	patch.CreatedAt = now
	patch.UpdatedAt = now
	if err := s.enrichmentPatches.Upsert(ctx, serviceID, patch); err != nil {
		return ServiceEnrichmentPatch{}, err
	}
	return patch, nil
}

func (s Service) GetEnrichmentPatch(ctx context.Context, serviceID string) (ServiceEnrichmentPatch, error) {
	var patch ServiceEnrichmentPatch
	err := s.enrichmentPatches.FindByService(ctx, serviceID, &patch)
	return patch, err
}

func (s Service) UpsertParserRule(ctx context.Context, serviceID string, rule ServiceParserRule) (ServiceParserRule, error) {
	if _, err := s.serviceRepo.Get(ctx, serviceID); err != nil {
		return ServiceParserRule{}, err
	}
	if strings.TrimSpace(rule.CollectorGroupID) == "" {
		var patch ServiceEnrichmentPatch
		if err := s.enrichmentPatches.FindByService(ctx, serviceID, &patch); err == nil {
			rule.CollectorGroupID = patch.CollectorGroupID
		}
	}
	if strings.TrimSpace(rule.CollectorGroupID) == "" {
		rule.CollectorGroupID = serviceID
	}
	rule.ServiceID = serviceID
	rule.ParseMode = firstNonEmpty(rule.ParseMode, "none")
	rule.ParseFrom = firstNonEmpty(rule.ParseFrom, "body")
	if rule.ID == "" {
		rule.ID = primitive.NewObjectID().Hex()
		rule.CreatedAt = time.Now().UTC()
	}
	if rule.Version == 0 {
		rule.Version = 1
	}
	if rule.Status == "" {
		rule.Status = "draft"
	}
	rule.UpdatedAt = time.Now().UTC()
	if err := s.parserRules.Upsert(ctx, serviceID, rule); err != nil {
		return ServiceParserRule{}, err
	}
	return rule, nil
}

func (s Service) GetParserRule(ctx context.Context, serviceID string) (ServiceParserRule, error) {
	var rule ServiceParserRule
	err := s.parserRules.FindByService(ctx, serviceID, &rule)
	return rule, err
}

func (s Service) PreviewParserRule(ctx context.Context, serviceID string, request ParserPreviewRequest) (ParserPreviewResult, error) {
	if _, err := s.serviceRepo.Get(ctx, serviceID); err != nil {
		return ParserPreviewResult{}, err
	}
	return PreviewParser(request), nil
}

func (s Service) GeneratePipelinePatch(ctx context.Context, serviceID string) (ServicePipelinePatch, error) {
	rule, err := s.GetParserRule(ctx, serviceID)
	if err != nil {
		return ServicePipelinePatch{}, err
	}
	patch, err := BuildPipelinePatch(rule)
	if err != nil {
		return ServicePipelinePatch{}, apperr.InvalidRequest(err.Error())
	}
	patch.ID = primitive.NewObjectID().Hex()
	now := time.Now().UTC()
	patch.CreatedAt = now
	patch.UpdatedAt = now
	if err := s.pipelinePatches.Upsert(ctx, serviceID, patch); err != nil {
		return ServicePipelinePatch{}, err
	}
	return patch, nil
}

func (s Service) ConfigSources(ctx context.Context, groupID string) (ConfigSources, error) {
	if _, err := s.collectorService.GetGroup(ctx, groupID); err != nil {
		return ConfigSources{}, err
	}
	sources := ConfigSources{
		ServiceEnrichmentPatches: []ServiceEnrichmentPatch{},
		ServicePipelinePatches:   []ServicePipelinePatch{},
		Warnings:                 []string{},
		Errors:                   []string{},
		SourceBreakdown:          []SourceBreakdown{},
	}
	if err := s.enrichmentPatches.FindByCollectorGroup(ctx, groupID, &sources.ServiceEnrichmentPatches); err != nil {
		return ConfigSources{}, err
	}
	if err := s.pipelinePatches.FindByCollectorGroup(ctx, groupID, &sources.ServicePipelinePatches); err != nil {
		return ConfigSources{}, err
	}
	validation := ValidateSources(sources)
	sources.RenderedYAML = validation.RenderedYAML
	sources.ConfigHash = validation.ConfigHash
	sources.Warnings = validation.Warnings
	sources.Errors = validation.Errors
	sources.SourceBreakdown = validation.SourceBreakdown
	return sources, nil
}

func (s Service) ValidateGroup(ctx context.Context, groupID string) (ValidationResult, error) {
	sources, err := s.ConfigSources(ctx, groupID)
	if err != nil {
		return ValidationResult{}, err
	}
	return ValidationResult{
		Valid:           len(sources.Errors) == 0,
		RenderedYAML:    sources.RenderedYAML,
		ConfigHash:      sources.ConfigHash,
		SourceBreakdown: sources.SourceBreakdown,
		Warnings:        sources.Warnings,
		Errors:          sources.Errors,
	}, nil
}

func serviceAttributes(service servicecatalog.Service) ServiceAttributes {
	return ServiceAttributes{
		ID:            service.ID,
		Name:          service.Name,
		CMDBServiceID: service.CMDBServiceID,
		BusinessID:    service.BusinessID,
		ApplicationID: service.ApplicationID,
		IdentityType:  service.IdentityType,
		Environment:   service.Environment,
		Cluster:       service.Cluster,
		Namespace:     service.Namespace,
		OwnerTeam:     service.OwnerTeam,
		AlertRoute:    service.AlertRoute,
	}
}

type ServicePipelineSources struct {
	ServiceID       string                     `json:"service_id"`
	Base            *CollectorPlatformTemplate `json:"base,omitempty"`
	Enrichment      *ServiceEnrichmentPatch    `json:"enrichment,omitempty"`
	Parser          *ServicePipelinePatch      `json:"parser,omitempty"`
	RenderedYAML    string                     `json:"rendered_yaml"`
	ConfigHash      string                     `json:"config_hash"`
	Warnings        []string                   `json:"warnings"`
	Errors          []string                   `json:"errors"`
	SourceBreakdown []SourceBreakdown          `json:"source_breakdown"`
}

func serviceBaseTemplateID(serviceID string) string {
	return "service-base:" + serviceID
}

func (s Service) UpsertServiceBaseConfig(ctx context.Context, serviceID string, baseYAML string) (CollectorPlatformTemplate, error) {
	if _, err := s.serviceRepo.Get(ctx, serviceID); err != nil {
		return CollectorPlatformTemplate{}, err
	}
	baseYAML = strings.TrimSpace(baseYAML)
	if baseYAML != "" {
		if _, err := parseYAMLDocument(baseYAML); err != nil {
			return CollectorPlatformTemplate{}, apperr.InvalidRequest(fmt.Sprintf("base_yaml 无效: %v", err))
		}
	}
	now := time.Now().UTC()
	id := serviceBaseTemplateID(serviceID)
	template := CollectorPlatformTemplate{}
	if err := s.templates.FindByID(ctx, id, &template); err != nil {
		template = CollectorPlatformTemplate{ID: id, Name: "service-processing-" + serviceID, Source: "service_processing_config", Status: "active", Version: 1, CreatedAt: now}
	} else {
		template.Version++
	}
	template.BaseYAML = baseYAML
	template.ConfigHash = HashYAML(baseYAML)
	template.UpdatedAt = now
	if template.CreatedAt.IsZero() {
		template.CreatedAt = now
	}
	if template.Version <= 0 {
		template.Version = 1
	}
	if err := s.templates.Update(ctx, id, template); err != nil {
		if err := s.templates.Insert(ctx, template); err != nil {
			return CollectorPlatformTemplate{}, err
		}
	}
	return template, nil
}

func (s Service) ServiceConfigSources(ctx context.Context, serviceID string) (ServicePipelineSources, error) {
	if _, err := s.serviceRepo.Get(ctx, serviceID); err != nil {
		return ServicePipelineSources{}, err
	}
	result := ServicePipelineSources{ServiceID: serviceID, Warnings: []string{}, Errors: []string{}, SourceBreakdown: []SourceBreakdown{}}
	var base CollectorPlatformTemplate
	if err := s.templates.FindByID(ctx, serviceBaseTemplateID(serviceID), &base); err == nil {
		result.Base = &base
	}
	var enrichment ServiceEnrichmentPatch
	if err := s.enrichmentPatches.FindByService(ctx, serviceID, &enrichment); err == nil {
		result.Enrichment = &enrichment
	}
	var parser ServicePipelinePatch
	if err := s.pipelinePatches.FindByService(ctx, serviceID, &parser); err == nil {
		result.Parser = &parser
	}
	configSources := ConfigSources{PlatformTemplate: result.Base}
	if result.Enrichment != nil {
		configSources.ServiceEnrichmentPatches = []ServiceEnrichmentPatch{*result.Enrichment}
	}
	if result.Parser != nil {
		configSources.ServicePipelinePatches = []ServicePipelinePatch{*result.Parser}
	}
	validation := ValidateSources(configSources)
	result.RenderedYAML = validation.RenderedYAML
	result.ConfigHash = validation.ConfigHash
	result.Warnings = validation.Warnings
	result.Errors = validation.Errors
	result.SourceBreakdown = validation.SourceBreakdown
	return result, nil
}

func (s Service) ValidateServicePipeline(ctx context.Context, serviceID string) (ValidationResult, error) {
	sources, err := s.ServiceConfigSources(ctx, serviceID)
	if err != nil {
		return ValidationResult{}, err
	}
	return ValidationResult{Valid: len(sources.Errors) == 0, RenderedYAML: sources.RenderedYAML, ConfigHash: sources.ConfigHash, SourceBreakdown: sources.SourceBreakdown, Warnings: sources.Warnings, Errors: sources.Errors}, nil
}
