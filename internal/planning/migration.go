package planning

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/autumnust/tack/internal/model"
	"gopkg.in/yaml.v3"
)

// MigrationResult reports what was pushed to Redis during a one-time import.
type MigrationResult struct {
	PlanItems      int // 1 if plan.yaml was found and pushed, else 0
	AnnotationRows int
	HibanaNotes    int
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

	return res, nil
}

