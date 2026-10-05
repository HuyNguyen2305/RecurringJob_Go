package service

import (
	"context"

	"recurringjob/internal/model"
)

// ServiceTypeStore is the storage the service type service needs.
type ServiceTypeStore interface {
	Create(ctx context.Context, s *model.ServiceType) error
	Get(ctx context.Context, id string) (*model.ServiceType, error)
	List(ctx context.Context) ([]model.ServiceType, error)
}

// ServiceTypeInput creates a service type; Description is optional.
type ServiceTypeInput struct {
	Name, Description string
}

// ServiceTypeService holds service type business rules.
type ServiceTypeService struct {
	types ServiceTypeStore
}

func NewServiceTypeService(types ServiceTypeStore) *ServiceTypeService {
	return &ServiceTypeService{types: types}
}

// CreateServiceType validates and saves a service type.
func (s *ServiceTypeService) CreateServiceType(ctx context.Context, in ServiceTypeInput) (*model.ServiceType, error) {
	name, err := requiredText("name", in.Name, MaxNameLen)
	if err != nil {
		return nil, err
	}
	description, err := optionalText("description", in.Description, MaxDescriptionText)
	if err != nil {
		return nil, err
	}
	t := &model.ServiceType{Name: name, Description: description}
	if err := s.types.Create(ctx, t); err != nil {
		return nil, err
	}
	return t, nil
}

// ListServiceTypes returns every service type by name.
func (s *ServiceTypeService) ListServiceTypes(ctx context.Context) ([]model.ServiceType, error) {
	return s.types.List(ctx)
}
