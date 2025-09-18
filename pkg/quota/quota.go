package quota

import (
	"context"
	"encoding/json"
	"errors"

	// "encoding/json"
	// "errors"
	"fmt"

	"github.com/segmentio/kafka-go"
	"github.com/segmentio/topicctl/pkg/admin"
	"github.com/segmentio/topicctl/pkg/config"
	"github.com/segmentio/topicctl/pkg/util"

	// "github.com/segmentio/topicctl/pkg/util"
	log "github.com/sirupsen/logrus"
)

type QuotaAdminConfig struct {
	ClusterConfig config.ClusterConfig
	DryRun        bool
	SkipConfirm   bool
	Delete        bool
	QuotaConfig   config.QuotaConfig
}

type QuotaAdmin struct {
	config      QuotaAdminConfig
	adminClient admin.Client

	clusterConfig config.ClusterConfig
	quotaConfig   config.QuotaConfig
}

func NewQuotaAdmin(
	ctx context.Context,
	adminClient admin.Client,
	creatorConfig QuotaAdminConfig,
) (*QuotaAdmin, error) {

	return &QuotaAdmin{
		config:        creatorConfig,
		adminClient:   adminClient,
		clusterConfig: creatorConfig.ClusterConfig,
		quotaConfig:   creatorConfig.QuotaConfig,
	}, nil
}

func (a *QuotaAdmin) Create(ctx context.Context) error {
	log.Info("Validating configs...")

	if err := a.clusterConfig.Validate(); err != nil {
		return err
	}

	if err := a.quotaConfig.Validate(); err != nil {
		return err
	}

	if err := config.CheckConsistency(a.quotaConfig.Meta, a.clusterConfig); err != nil {
		return err
	}

	log.Info("Checking if quotas already exist...")

	client := a.adminClient.GetConnector().KafkaClient

	describeResponse, err := client.DescribeClientQuotas(ctx, &kafka.DescribeClientQuotasRequest{})
	if err != nil {
		return fmt.Errorf("error getting existing quotas: %v", err)
	}

	existingQuotas := ToConfigQuotas(describeResponse.Entries)

	log.Infof("Have %d existing quotas",len(existingQuotas))

	var effectiveProvidedQuotas []config.Quota
	providedQuotas := a.quotaConfig.Spec.Quotas
	log.Infof("Have %d provided quotas", len(providedQuotas))

	// only keep quotas with effective changes
	for _, entry := range providedQuotas{
		ops := []config.QuotaOperation{}
		for _, op := range entry.Operations {		
			if tryGetExistingQuota(entry.Entities, op.Key, op.Value, existingQuotas) == nil {
				// if we don't find an exact match (by entities, key and value), then this is really a new quota
				ops = append(ops, op)
			}
		}
		if len(ops) > 0 {
			effectiveProvidedQuotas = append(effectiveProvidedQuotas, config.Quota{
				Entities: entry.Entities,
				Operations: ops,
			})
		}		
	}

	removeQuotas := []config.Quota{}
	if a.config.Delete {
		for _, entry := range existingQuotas {
			keysToRemove := []string{}
			for _, op := range entry.Operations {
				maybeProvidedQuota := tryGetProvidedQuota(entry.Entities, op.Key, providedQuotas)
				if maybeProvidedQuota == nil {
					// if we don't find a match by entities and key then this quota should be removed
					keysToRemove = append(keysToRemove, op.Key)
				}						
			}
			if len(keysToRemove) == 0 {
				continue
			}
			removeQuoteEntry := toRemoveQuota(entry.Entities, keysToRemove)
			removeQuotas = append(removeQuotas, removeQuoteEntry)
		}
		log.Infof("Have %d quotas to remove", len(removeQuotas))
	}

	if len(effectiveProvidedQuotas) + len(removeQuotas) == 0 {
		log.Info("No quotas to create/alter")
		return nil
	}

	alterQuotas := []kafka.AlterClientQuotaEntry{}
	for _, q := range effectiveProvidedQuotas{
		alterQuotas = append(alterQuotas, q.ToAlterQuotaEntry(false))
	}
	for _, q := range removeQuotas{
		alterQuotas = append(alterQuotas, q.ToAlterQuotaEntry(true))
	}

	if a.config.DryRun {
		log.Infof(
			"Would create/alter %d quotas with config:\n%s",
			len(effectiveProvidedQuotas),
			formatAlterQuotasConfig(alterQuotas),
		)
		return nil
	}	

	log.Infof("Will create/alter %d quotas with config:\n%s", len(effectiveProvidedQuotas), formatAlterQuotasConfig(alterQuotas))
	ok, _ := util.Confirm("OK to continue?", a.config.SkipConfirm)
	if !ok {
		return errors.New("Stopping because of user response")
	}

	alterResponse, err := client.AlterClientQuotas(ctx, &kafka.AlterClientQuotasRequest{
		Entries:      alterQuotas,
		ValidateOnly: a.config.DryRun,
	})
	if err != nil {
		return fmt.Errorf("error altering quotas: %v", err)
	}
	log.Infof("Altered %d quotas:\n", len(alterResponse.Entries))

	return nil
}


func formatAlterQuotasConfig(config []kafka.AlterClientQuotaEntry) string {
	content, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		log.Warnf("Error marshalling quotas config: %+v", err)
		return "Error"
	}

	return string(content)
}

func formatQuotas(config []config.Quota) string {
	content, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		log.Warnf("Error marshalling quotas config: %+v", err)
		return "Error"
	}

	return string(content)
}

func tryGetProvidedQuota(
	existingQuotaEntities []config.QuotaEntity,
	operationKey string,
	providedQuotas []config.Quota) *config.Quota {
	for _, providedQuota := range providedQuotas {
		if len(existingQuotaEntities) != len(providedQuota.Entities) {
			continue
		}
		matchedAllEntities := true
		for _, existingEntity := range existingQuotaEntities {
			foundMatch := false
			for _, providedEntity := range providedQuota.Entities {
				if existingEntity.EntityType == providedEntity.EntityType &&
					existingEntity.EntityName == providedEntity.EntityName {
					foundMatch = true
					break
				}
			}
			if !foundMatch {
				matchedAllEntities = false
				break
			}
		}
		if !matchedAllEntities {
			continue
		}
		foundValue := false
		for _, op := range providedQuota.Operations {
			if op.Key == operationKey {
				foundValue = true
				break
			}
		}
		if foundValue {
			return &providedQuota
		}
	}
	return nil
}

func tryGetExistingQuota(
	providedQuotaEntities []config.QuotaEntity, 
	operationKey string,
	operationValue float64,
	existingQuotas []config.Quota) *config.Quota {
	for _, existingQuota := range existingQuotas {
		if len(providedQuotaEntities) != len(existingQuota.Entities) {
			continue
		}
		matchedAllEntities := true
		for _, providedEntity := range providedQuotaEntities {
			foundMatch := false
			for _, existingEntity := range existingQuota.Entities {
				if providedEntity.EntityType == existingEntity.EntityType &&
					providedEntity.EntityName == existingEntity.EntityName {
					foundMatch = true
					break
				}
			}
			if !foundMatch {
				matchedAllEntities = false
				break
			}
		}
		if !matchedAllEntities {
			continue
		}
		foundValue := false
		for _, op := range existingQuota.Operations {
			if op.Key == operationKey && op.Value == operationValue {
				foundValue = true
				break
			}
		}
		if foundValue {
			return &existingQuota
		}
	}
	return nil
}

func toRemoveQuota(entities []config.QuotaEntity, keys []string) config.Quota {	
	operations := []config.QuotaOperation{}
	for _, key := range keys {
		operations = append(operations, config.QuotaOperation{
			Key:   key,
		})
	}
	return config.Quota{
		Entities: entities,
		Operations: operations,
	}
}

func ToConfigQuotas(responseEntries []kafka.DescribeClientQuotasResponseQuotas) []config.Quota {
	entries := []config.Quota{}

	for _, quota := range responseEntries {
		entities := []config.QuotaEntity{}
		for _, entity := range quota.Entities {
			entities = append(entities, config.QuotaEntity{
				EntityType: entity.EntityType,
				EntityName: entity.EntityName,
			})
		}
		ops := []config.QuotaOperation{}
		for _, operation := range quota.Values {
			ops = append(ops, config.QuotaOperation{
				Key:   operation.Key,
				Value: operation.Value,
			})
		}

		entry := config.Quota{
			Entities: entities,
			Operations: ops,
		}
		entries = append(entries, entry)
	}
	return entries
}
