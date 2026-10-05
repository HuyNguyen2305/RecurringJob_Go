package service_test

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"testing"

	"recurringjob/internal/common/apperror"
	"recurringjob/internal/model"
	"recurringjob/internal/service"
)

// memServiceTypes is an in-memory service.ServiceTypeStore (and ServiceTypeGetter).
type memServiceTypes struct {
	rows      map[string]*model.ServiceType
	seq       int
	createErr error
	listErr   error
}

func newMemServiceTypes() *memServiceTypes {
	return &memServiceTypes{rows: map[string]*model.ServiceType{}}
}

func (m *memServiceTypes) Create(_ context.Context, s *model.ServiceType) error {
	if m.createErr != nil {
		return m.createErr
	}
	m.seq++
	s.ID = uid(fmt.Sprintf("%02x", 0xc0+m.seq))
	cp := *s
	m.rows[s.ID] = &cp
	return nil
}

func (m *memServiceTypes) Get(_ context.Context, id string) (*model.ServiceType, error) {
	s, ok := m.rows[id]
	if !ok {
		return nil, apperror.NotFound("service type not found")
	}
	cp := *s
	return &cp, nil
}

func (m *memServiceTypes) List(context.Context) ([]model.ServiceType, error) {
	if m.listErr != nil {
		return nil, m.listErr
	}
	out := []model.ServiceType{}
	for _, s := range m.rows {
		out = append(out, *s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func TestServiceTypes(t *testing.T) {
	ctx := context.Background()

	t.Run("a name is enough; the description is optional", func(t *testing.T) {
		store := newMemServiceTypes()
		s := service.NewServiceTypeService(store)
		got, err := s.CreateServiceType(ctx, service.ServiceTypeInput{Name: "  Window cleaning "})
		if err != nil || got.Name != "Window cleaning" || got.Description != nil || got.ID == "" || len(store.rows) != 1 {
			t.Fatalf("got=%+v err=%v", got, err)
		}
		got, err = s.CreateServiceType(ctx, service.ServiceTypeInput{Name: "Gutters", Description: " Inside and out "})
		if err != nil || got.Description == nil || *got.Description != "Inside and out" {
			t.Fatalf("got=%+v err=%v", got, err)
		}
	})

	t.Run("rejections save nothing", func(t *testing.T) {
		for name, in := range map[string]service.ServiceTypeInput{
			"no name":              {},
			"blank name":           {Name: "  "},
			"name too long":        {Name: strings.Repeat("a", service.MaxNameLen+1)},
			"description too long": {Name: "x", Description: strings.Repeat("d", service.MaxDescriptionText+1)},
		} {
			store := newMemServiceTypes()
			if _, err := service.NewServiceTypeService(store).CreateServiceType(ctx, in); statusOf(t, err) != 400 {
				t.Errorf("%s: %v", name, err)
			}
			if len(store.rows) != 0 {
				t.Errorf("%s: a service type was saved", name)
			}
		}
	})

	t.Run("store errors are returned as is", func(t *testing.T) {
		boom := errors.New("db down")
		store := newMemServiceTypes()
		store.createErr, store.listErr = boom, boom
		s := service.NewServiceTypeService(store)
		if _, err := s.CreateServiceType(ctx, service.ServiceTypeInput{Name: "x"}); !errors.Is(err, boom) {
			t.Errorf("create: %v", err)
		}
		if _, err := s.ListServiceTypes(ctx); !errors.Is(err, boom) {
			t.Errorf("list: %v", err)
		}
	})

	t.Run("list returns them by name", func(t *testing.T) {
		s := service.NewServiceTypeService(newMemServiceTypes())
		for _, n := range []string{"Gutters", "Windows", "Awnings"} {
			if _, err := s.CreateServiceType(ctx, service.ServiceTypeInput{Name: n}); err != nil {
				t.Fatal(err)
			}
		}
		list, err := s.ListServiceTypes(ctx)
		if err != nil || len(list) != 3 || list[0].Name != "Awnings" || list[2].Name != "Windows" {
			t.Fatalf("list=%+v err=%v", list, err)
		}
	})
}
