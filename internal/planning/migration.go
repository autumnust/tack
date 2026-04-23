package planning

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/autumnust/tack/internal/model"
	"gopkg.in/yaml.v3"
)

// MigrationResult reports what was pushed to Redis during a one-time import.
type MigrationResult struct {
	PlanItems      int // 1 if plan.yaml was found and pushed, else 0
	AnnotationRows int
	HibanaNotes    int
	UsageEntries   int
	Recaps         int
}

// MigrateLeisureVault reads plan.yaml and annotations.yaml from srcDir and
// pushes their contents to Redis via this Store's backend. Scratch notes from
// the source plan are RPUSHed into tack:hibana individually.
//
// By default the migration aborts if tack:plan already exists in Redis, to
// avoid clobbering cloud data; pass force=true to overwrite.
func (s *Store) MigrateLeisureVault(srcDir string, force bool) (MigrationResult, error) {
	var res MigrationResult
	if !s.RedisEnabled() {
		return res, fmt.Errorf("redis backend is not configured")
	}

	ctx, cancel := s.ctx()
	defer cancel()

	if !force {
		if _, ok, err := s.redis.Get(ctx, keyPlan); err != nil {
			return res, fmt.Errorf("probe %s: %w", keyPlan, err)
		} else if ok {
			return res, fmt.Errorf("%s already exists in redis; pass --force to overwrite", keyPlan)
		}
	}

	planPath := filepath.Join(srcDir, "plan.yaml")
	annPath := filepath.Join(srcDir, "annotations.yaml")

	var plan model.Plan
	if data, err := os.ReadFile(planPath); err == nil {
		if err := yaml.Unmarshal(data, &plan); err != nil {
			return res, fmt.Errorf("parse %s: %w", planPath, err)
		}
		planCopy := plan
		planCopy.Scratch = nil
		blob, err := json.Marshal(&planCopy)
		if err != nil {
			return res, err
		}
		if err := s.redis.Set(ctx, keyPlan, string(blob)); err != nil {
			return res, fmt.Errorf("set %s: %w", keyPlan, err)
		}
		res.PlanItems = 1

		// Hibana list: reset then bulk RPUSH.
		if err := s.redis.Del(ctx, keyHibana); err != nil {
			return res, fmt.Errorf("del %s: %w", keyHibana, err)
		}
		if len(plan.Scratch) > 0 {
			vals := make([]string, 0, len(plan.Scratch))
			for _, n := range plan.Scratch {
				b, err := json.Marshal(n)
				if err != nil {
					continue
				}
				vals = append(vals, string(b))
			}
			if err := s.redis.RPush(ctx, keyHibana, vals...); err != nil {
				return res, fmt.Errorf("rpush %s: %w", keyHibana, err)
			}
			res.HibanaNotes = len(vals)
		}
	} else if !os.IsNotExist(err) {
		return res, fmt.Errorf("read %s: %w", planPath, err)
	}

	var ann model.Annotations
	if data, err := os.ReadFile(annPath); err == nil {
		if err := yaml.Unmarshal(data, &ann); err != nil {
			return res, fmt.Errorf("parse %s: %w", annPath, err)
		}
		blob, err := json.Marshal(&ann)
		if err != nil {
			return res, err
		}
		if err := s.redis.Set(ctx, keyAnnotations, string(blob)); err != nil {
			return res, fmt.Errorf("set %s: %w", keyAnnotations, err)
		}
		res.AnnotationRows = len(ann.Items)
	} else if !os.IsNotExist(err) {
		return res, fmt.Errorf("read %s: %w", annPath, err)
	}

	// Initialize rev counters if unset so future writes start from a known point.
	for _, k := range []string{keyPlanRev, keyAnnRev} {
		if _, ok, _ := s.redis.Get(ctx, k); !ok {
			if _, err := s.redis.Incr(ctx, k); err != nil {
				return res, fmt.Errorf("init %s: %w", k, err)
			}
		}
	}

	// Usage log → tack:usage list.
	usagePath := filepath.Join(srcDir, "usage.log")
	if data, err := os.ReadFile(usagePath); err == nil {
		if force {
			if err := s.redis.Del(ctx, keyUsage); err != nil {
				return res, fmt.Errorf("del %s: %w", keyUsage, err)
			}
		}
		var vals []string
		for _, line := range strings.Split(string(data), "\n") {
			parts := strings.SplitN(line, "\t", 2)
			if len(parts) != 2 {
				continue
			}
			ts, _ := time.Parse(time.RFC3339, parts[0])
			if ts.IsZero() {
				ts = time.Now()
			}
			b, err := json.Marshal(UsageEntry{TS: ts, Cmd: parts[1]})
			if err != nil {
				continue
			}
			vals = append(vals, string(b))
		}
		if len(vals) > 0 {
			if err := s.redis.RPush(ctx, keyUsage, vals...); err != nil {
				return res, fmt.Errorf("rpush %s: %w", keyUsage, err)
			}
			res.UsageEntries = len(vals)
		}
	} else if !os.IsNotExist(err) {
		return res, fmt.Errorf("read %s: %w", usagePath, err)
	}

	// Recaps dir → per-name keys + index.
	recapsPath := filepath.Join(srcDir, "recaps")
	if entries, err := os.ReadDir(recapsPath); err == nil {
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
				continue
			}
			name := strings.TrimSuffix(e.Name(), ".md")
			body, err := os.ReadFile(filepath.Join(recapsPath, e.Name()))
			if err != nil {
				continue
			}
			if err := s.redis.Set(ctx, recapKey(name), string(body)); err != nil {
				return res, fmt.Errorf("set %s: %w", recapKey(name), err)
			}
			if err := s.redis.SAdd(ctx, keyRecapsIndex, name); err != nil {
				return res, fmt.Errorf("sadd %s: %w", keyRecapsIndex, err)
			}
			res.Recaps++
		}
	} else if !os.IsNotExist(err) {
		return res, fmt.Errorf("read %s: %w", recapsPath, err)
	}

	return res, nil
}

