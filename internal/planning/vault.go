package planning

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/autumnust/tack/internal/model"
)

// MinReflectionChars is the floor (counting non-whitespace runes) for a
// reflection to count as "written." Below this, :reflect refuses to seal.
const MinReflectionChars = 10

// VaultWriter writes sealed monthly target files into an Obsidian vault.
// One file per month at <Vault>/<MonthlySubdir>/<YYYY-MM>.md.
type VaultWriter struct {
	Vault         string
	MonthlySubdir string
}

// NewVaultWriter expands ~, validates the vault directory, and defaults
// MonthlySubdir to "monthly" when empty.
func NewVaultWriter(vault, subdir string) (*VaultWriter, error) {
	if vault == "" {
		return nil, fmt.Errorf("vault path is empty")
	}
	if strings.HasPrefix(vault, "~") {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, err
		}
		vault = filepath.Join(home, vault[1:])
	}
	info, err := os.Stat(vault)
	if err != nil {
		return nil, fmt.Errorf("vault %q: %w", vault, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("vault %q is not a directory", vault)
	}
	if subdir == "" {
		subdir = "monthly"
	}
	return &VaultWriter{Vault: vault, MonthlySubdir: subdir}, nil
}

// MonthlyDir returns the absolute directory holding sealed month files.
func (v *VaultWriter) MonthlyDir() string {
	return filepath.Join(v.Vault, v.MonthlySubdir)
}

// FilePath returns the path for a given month (YYYY-MM).
func (v *VaultWriter) FilePath(month string) string {
	return filepath.Join(v.MonthlyDir(), month+".md")
}

// SealMonth writes <vault>/<subdir>/<month>.md with the targets and the
// reflection. Refuses to seal a reflection shorter than
// MinReflectionChars non-whitespace runes. Overwrites any existing
// file: re-sealing the same month is the cheapest way to fix a typo.
func (v *VaultWriter) SealMonth(month string, targets []model.MonthlyTarget, reflection string) error {
	if !ValidMonth(month) {
		return fmt.Errorf("invalid month %q (want YYYY-MM)", month)
	}
	if CountNonWhitespace(reflection) < MinReflectionChars {
		return fmt.Errorf("reflection too short (need at least %d non-whitespace chars)", MinReflectionChars)
	}
	if err := os.MkdirAll(v.MonthlyDir(), 0755); err != nil {
		return err
	}
	var sb strings.Builder
	sb.WriteString("---\n")
	fmt.Fprintf(&sb, "month: %s\n", month)
	fmt.Fprintf(&sb, "sealed_at: %s\n", time.Now().Format(time.RFC3339))
	sb.WriteString("---\n\n")
	fmt.Fprintf(&sb, "# Reflection: %s\n\n", month)
	sb.WriteString("## Targets\n")
	for _, t := range targets {
		check := "[ ]"
		if t.Done {
			check = "[x]"
		}
		text := strings.ReplaceAll(strings.TrimSpace(t.Text), "\n", " ")
		fmt.Fprintf(&sb, "- %s %s\n", check, text)
	}
	sb.WriteString("\n## Reflection\n")
	sb.WriteString(strings.TrimRight(reflection, "\n"))
	sb.WriteString("\n")
	return os.WriteFile(v.FilePath(month), []byte(sb.String()), 0644)
}

// PendingMonths returns the distinct months strictly earlier than
// `current` that still have targets in the active plan. Sorted ascending
// so :reflect can walk the oldest first.
func PendingMonths(targets []model.MonthlyTarget, current string) []string {
	seen := map[string]bool{}
	for _, t := range targets {
		if t.Month != "" && t.Month < current {
			seen[t.Month] = true
		}
	}
	out := make([]string, 0, len(seen))
	for m := range seen {
		out = append(out, m)
	}
	sort.Strings(out)
	return out
}

// CurrentMonth returns the local-time YYYY-MM bucket.
func CurrentMonth() string { return time.Now().Format("2006-01") }

// ValidMonth reports whether s is a parseable YYYY-MM string.
func ValidMonth(s string) bool {
	if len(s) != 7 || s[4] != '-' {
		return false
	}
	_, err := time.Parse("2006-01", s)
	return err == nil
}

// CountNonWhitespace returns the count of non-whitespace runes in s.
func CountNonWhitespace(s string) int {
	n := 0
	for _, r := range s {
		switch r {
		case ' ', '\t', '\n', '\r':
		default:
			n++
		}
	}
	return n
}
