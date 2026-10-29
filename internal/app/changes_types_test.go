package app

import "testing"

// Changed files sort as Lody's: only a strong signal takes a file out of
// code.
func TestClassifyChange(t *testing.T) {
	for file, want := range map[string]string{
		"internal/app/chat.go":           "code",
		"internal/app/chat_test.go":      "test",
		"src/__tests__/view.tsx":         "test",
		"src/view.spec.ts":               "test",
		"README.md":                      "doc",
		"docs/guide/intro.txt":           "doc",
		"LICENSE":                        "doc",
		".github/workflows/release.yml":  "dev",
		"package-lock.json":              "dev",
		"vite.config.ts":                 "dev",
		"tsconfig.app.json":              "dev",
		".env.local":                     "dev",
		"src/types.d.ts":                 "code",
		"cmd/lody/main.go":               "code",
		"packages/ui/src/test-utils.tsx": "test",
		"./src/app.ts":                   "code",
	} {
		if got := classifyChange(file); got != want {
			t.Errorf("%s: %s, want %s", file, got, want)
		}
	}
}
