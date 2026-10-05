package handler_test

import (
	"context"
	"testing"

	"github.com/gin-gonic/gin"

	"recurringjob/internal/common/apperror"
	"recurringjob/internal/handler"
	"recurringjob/internal/model"
	"recurringjob/internal/service"
)

type fakeServiceTypes struct {
	err    error
	called bool
	in     service.ServiceTypeInput
}

func (f *fakeServiceTypes) CreateServiceType(_ context.Context, in service.ServiceTypeInput) (*model.ServiceType, error) {
	f.in, f.called = in, true
	if f.err != nil {
		return nil, f.err
	}
	return &model.ServiceType{ID: "s-1", Name: in.Name}, nil
}

func (f *fakeServiceTypes) ListServiceTypes(context.Context) ([]model.ServiceType, error) {
	f.called = true
	if f.err != nil {
		return nil, f.err
	}
	return []model.ServiceType{{ID: "s-1", Name: "Window cleaning"}}, nil
}

func serviceTypeEngine(f *fakeServiceTypes) *gin.Engine {
	return newEngine(func(r *gin.Engine) {
		h := handler.NewServiceTypeHandler(f)
		r.POST("/service-types", h.Create)
		r.GET("/service-types", h.List)
	})
}

func TestServiceTypeHandler(t *testing.T) {
	t.Run("create: success envelope and decoded input", func(t *testing.T) {
		f := &fakeServiceTypes{}
		w := do(serviceTypeEngine(f), "POST", "/service-types", `{"name":"Window cleaning","description":"Inside and out"}`)
		if w.Code != 200 {
			t.Fatalf("status %d: %s", w.Code, w.Body)
		}
		data := decode(t, w)["data"].(map[string]any)
		if data["id"] != "s-1" || data["name"] != "Window cleaning" || data["description"] != nil {
			t.Fatalf("data %v", data)
		}
		if f.in != (service.ServiceTypeInput{Name: "Window cleaning", Description: "Inside and out"}) {
			t.Fatalf("input %+v", f.in)
		}
	})

	t.Run("create: bad bodies are 400 and never reach the service", func(t *testing.T) {
		for name, body := range map[string]string{"no name": `{}`, "bad json": `{`, "wrong type": `{"name":1}`} {
			f := &fakeServiceTypes{}
			if w := do(serviceTypeEngine(f), "POST", "/service-types", body); w.Code != 400 || f.called {
				t.Errorf("%s: status %d called=%v", name, w.Code, f.called)
			}
		}
	})

	t.Run("list returns the types", func(t *testing.T) {
		f := &fakeServiceTypes{}
		w := do(serviceTypeEngine(f), "GET", "/service-types", "")
		if got := decode(t, w)["data"].([]any); w.Code != 200 || len(got) != 1 || got[0].(map[string]any)["name"] != "Window cleaning" {
			t.Fatalf("status %d body %s", w.Code, w.Body)
		}
	})

	t.Run("service errors map through the middleware", func(t *testing.T) {
		f := &fakeServiceTypes{err: apperror.Validation("name must be at most 255 characters")}
		if w := do(serviceTypeEngine(f), "POST", "/service-types", `{"name":"x"}`); w.Code != 400 {
			t.Errorf("create: %d", w.Code)
		}
		if w := do(serviceTypeEngine(&fakeServiceTypes{err: context.DeadlineExceeded}), "GET", "/service-types", ""); w.Code != 500 {
			t.Errorf("list: %d", w.Code)
		}
	})
}
