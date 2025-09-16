package config

import (
	"github.com/segmentio/kafka-go"
)

type QuotaConfig struct {
	Meta ResourceMeta `json:"meta"`
	Spec QuotaSpec    `json:"spec"`
}

type QuotaSpec struct {
	Quotas []Quota `json:"quotas"`
}

type Quota struct {
	Entities   []QuotaEntity    `json:"entities"`
	Operations []QuotaOperation `json:"ops"`
}

type QuotaEntity struct {
	EntityType string `json:"type"`
	EntityName string `json:"name"`
}

type QuotaOperation struct {
	Key   string `json:"key"`
	Value int32  `json:"value"`
}

// Validate evaluates whether the ACL config is valid.
func (a *QuotaConfig) Validate() error {
	var err error

	err = a.Meta.Validate()

	// TODO: add more validation

	return err
}

func (quota Quota) ToAlterQuotaEntry(mustRemove bool) kafka.AlterClientQuotaEntry {

	entities := []kafka.AlterClientQuotaEntity{}
	for _, entity := range quota.Entities {
		entities = append(entities, kafka.AlterClientQuotaEntity{
			EntityType: entity.EntityType,
			EntityName: entity.EntityName,
		})
	}
	ops := []kafka.AlterClientQuotaOps{}
	for _, operation := range quota.Operations {
		ops = append(ops, kafka.AlterClientQuotaOps{
			Key:   operation.Key,
			Value: float64(operation.Value),
			Remove: mustRemove,
		})
	}

	return kafka.AlterClientQuotaEntry{
		Entities: entities,
		Ops:      ops,
	}
}
