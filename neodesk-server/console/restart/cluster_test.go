package restart

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestAClusterInstanceDoesNotRestartItself(t *testing.T) {
	_, err := ClusterRestart(context.Background())
	if !errors.Is(err, ErrClusterRestart) || !strings.Contains(err.Error(), "kubectl rollout restart") {
		t.Fatalf("error %v", err)
	}
}
