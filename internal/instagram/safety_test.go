package instagram

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSeenMutationIsNeverCalled(t *testing.T) {
	t.Parallel()

	for _, source := range []string{"cmd", "internal"} {
		root := filepath.Join("..", "..", source)
		err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || filepath.Ext(path) != ".go" || d.Name() == "safety_test.go" {
				return nil
			}
			b, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			if strings.Contains(string(b), "PolarisStoriesV3SeenMutation") {
				t.Fatalf("seen mutation reference found in %s", path)
			}
			return nil
		})
		if err != nil {
			t.Fatalf("walk source: %v", err)
		}
	}
}
