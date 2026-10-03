// specs/models-perf: in-memory-only cache (dies with process).
package tui

import (
	"sort"
	"strings"
	"time"

	"likha/internal/model"
)

const modelsCacheTTL = 5 * time.Minute

type modelsCache struct {
	sections  map[string]modelsSection // key: provider canonical Name
	fetchedAt time.Time
}

func (c *modelsCache) fresh() bool {
	if c == nil {
		return false
	}
	return len(c.sections) > 0 && time.Since(c.fetchedAt) < modelsCacheTTL
}

func (c *modelsCache) upsert(s modelsSection) {
	if c.sections == nil {
		c.sections = make(map[string]modelsSection)
	}
	c.sections[s.provider.Name] = s
	c.fetchedAt = time.Now()
}

func (c *modelsCache) removeNotIn(keep map[string]bool) {
	if c == nil || keep == nil {
		return
	}
	for k := range c.sections {
		if !keep[k] {
			delete(c.sections, k)
		}
	}
}

func (c *modelsCache) snapshot(activeCanonical, activeBase string, keep map[string]bool) []modelsSection {
	if c == nil || len(c.sections) == 0 {
		return nil
	}
	allowed := func(k string) bool {
		return keep == nil || keep[k]
	}
	inTable := make(map[string]bool, len(model.Providers))
	for _, p := range model.Providers {
		inTable[p.Name] = true
	}
	// (1) active first: BaseURL match wins, else canonical name match.
	var active *modelsSection
	if activeBase != "" {
		for _, p := range model.Providers {
			s, ok := c.sections[p.Name]
			if ok && s.provider.BaseURL == activeBase && allowed(s.provider.Name) {
				cp := s
				active = &cp
				break
			}
		}
		if active == nil {
			var keys []string
			for k, s := range c.sections {
				if !inTable[k] && s.provider.BaseURL == activeBase && allowed(s.provider.Name) {
					keys = append(keys, k)
				}
			}
			sort.Strings(keys)
			if len(keys) > 0 {
				cp := c.sections[keys[0]]
				active = &cp
			}
		}
	}
	if active == nil && activeCanonical != "" {
		for _, p := range model.Providers {
			s, ok := c.sections[p.Name]
			if ok && strings.EqualFold(s.provider.Name, activeCanonical) && allowed(s.provider.Name) {
				cp := s
				active = &cp
				break
			}
		}
		if active == nil {
			var keys []string
			for k, s := range c.sections {
				if !inTable[k] && strings.EqualFold(s.provider.Name, activeCanonical) && allowed(s.provider.Name) {
					keys = append(keys, k)
				}
			}
			sort.Strings(keys)
			if len(keys) > 0 {
				cp := c.sections[keys[0]]
				active = &cp
			}
		}
	}
	var out []modelsSection
	activeKey := ""
	if active != nil {
		out = append(out, *active)
		activeKey = active.provider.Name
	}
	// (2) model.Providers table order (skip active, skip absent).
	for _, p := range model.Providers {
		if p.Name == activeKey {
			continue
		}
		s, ok := c.sections[p.Name]
		if !ok {
			continue
		}
		if !allowed(p.Name) {
			continue
		}
		out = append(out, s)
	}
	// (3) leftovers sorted by DisplayName.
	var rest []modelsSection
	for k, s := range c.sections {
		if k == activeKey || inTable[k] {
			continue
		}
		if !allowed(k) {
			continue
		}
		rest = append(rest, s)
	}
	sort.Slice(rest, func(i, j int) bool { return rest[i].provider.DisplayName < rest[j].provider.DisplayName })
	out = append(out, rest...)
	if len(out) == 0 {
		return nil
	}
	return out
}
