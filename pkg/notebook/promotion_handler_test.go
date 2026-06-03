package notebook

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

type fakePromotionService struct {
	preview  *PromotionPreview
	validate *PromotionValidation
	export   *PromotionExportResult
}

func (f fakePromotionService) Preview(context.Context, string, PromotionTarget) (*PromotionPreview, error) {
	return f.preview, nil
}

func (f fakePromotionService) Validate(context.Context, string, PromotionTarget) (*PromotionValidation, error) {
	return f.validate, nil
}

func (f fakePromotionService) Export(context.Context, string, PromotionTarget) (*PromotionExportResult, error) {
	return f.export, nil
}

func (f fakePromotionService) ListExports(context.Context, string) ([]*PromotionExportRecord, error) {
	return []*PromotionExportRecord{
		{
			PromotionExportResult: *f.export,
			ManifestPath:          "/tmp/demo.json",
		},
	}, nil
}

func (f fakePromotionService) DownloadExport(context.Context, string, string) (*PromotionDownload, error) {
	return &PromotionDownload{
		Name:        "demo.yaml",
		ContentType: "text/yaml; charset=utf-8",
		Reader:      io.NopCloser(strings.NewReader("promotion: {}\n")),
	}, nil
}

func TestPromotionRoutes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := fakePromotionService{
		preview: &PromotionPreview{
			Name:   "demo",
			Target: PromotionTargetRepo,
			Validation: PromotionValidation{
				Status:   "ok",
				Messages: []string{"ready"},
			},
			Draft: "promotion:\n  name: demo\n",
		},
		validate: &PromotionValidation{Status: "warning", Messages: []string{"check"}},
		export: &PromotionExportResult{
			PromotionPreview: PromotionPreview{
				Name:   "demo",
				Target: PromotionTargetObjectStore,
			},
			BundlePath: "/tmp/demo.yaml",
			ObjectKey:  "promotions/demo/demo.yaml",
		},
	}
	router := gin.New()
	NewHandler(HandlerDeps{Promotions: svc}).RegisterRoutes(router.Group(""))

	req := httptest.NewRequest(http.MethodGet, "/api/notebooks/demo/promotion?target=repo", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("preview status = %d, want %d", rec.Code, http.StatusOK)
	}

	req = httptest.NewRequest(http.MethodPost, "/api/notebooks/demo/promotion/validate", nil)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("validate status = %d, want %d", rec.Code, http.StatusOK)
	}

	req = httptest.NewRequest(http.MethodPost, "/api/notebooks/demo/promotion/export", nil)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("export status = %d, want %d", rec.Code, http.StatusOK)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/notebooks/demo/promotion/exports", nil)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("list status = %d, want %d", rec.Code, http.StatusOK)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/notebooks/demo/promotion/exports/demo.yaml", nil)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("download status = %d, want %d", rec.Code, http.StatusOK)
	}
}
