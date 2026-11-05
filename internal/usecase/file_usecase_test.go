package usecase_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/willydocksha/go-clean-arch/internal/domain"
	"github.com/willydocksha/go-clean-arch/internal/usecase"
)

// without s3 every storage call is 503 but parsing still works
func TestFileUsecaseWithoutStorage(t *testing.T) {
	uc := usecase.NewFileUsecase(nil, time.Minute, time.Second)
	ctx := context.Background()

	// storage disabled
	if _, err := uc.PresignDownload(ctx, "a/b.txt", ""); !errors.Is(err, domain.ErrUnavailable) {
		t.Fatalf("want unavailable, got %v", err)
	}

	// parse csv with row limit
	sheet, err := uc.Parse(strings.NewReader("a,b\n1,2\n3,4\n5,6\n"), "data.csv", 2)
	if err != nil {
		t.Fatal(err)
	}
	if sheet.Total != 3 || len(sheet.Rows) != 2 || sheet.Rows[1]["b"] != "4" {
		t.Fatalf("unexpected sheet: %+v", sheet)
	}

	// unsupported extension
	if _, err := uc.Parse(strings.NewReader("x"), "data.pdf", 0); !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatalf("want invalid input, got %v", err)
	}
}
