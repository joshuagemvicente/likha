// Command gen regenerates internal/model/catalog/models.json
// (specs/model-metadata): it reads the models.dev catalog, keeps Likha's
// predefined providers and their tool-capable models, applies the
// hand-checked overrides.json, writes the file deterministically, and prints
// a change report to review before committing. Run it with
// `go generate ./internal/model/catalog`; it writes nothing on any error.
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"likha/internal/model"
	"likha/internal/model/catalog"
)

// DefaultUpstream is the models.dev catalog (MIT, anomalyco/models.dev).
const DefaultUpstream = "https://models.dev/api.json"

// upstreamIDs maps every Likha provider to its models.dev provider ID. An
// empty value means the provider is curated only (overrides.json). Adding a
// provider to model.Providers without a row here fails generation, so the
// mapping is always a deliberate choice.
var upstreamIDs = map[string]string{
	"openai":       "openai",
	"openrouter":   "openrouter",
	"bedrock":      "amazon-bedrock",
	"dialagram":    "", // not in models.dev; no verified rows yet
	"opencode-zen": "opencode",
	"opencode-go":  "opencode-go",
	"chatgpt":      "", // ChatGPT plan billing; curated in overrides.json
	"groq":         "groq",
	"xai":          "xai",
	"together":     "togetherai",
	"mistral":      "mistral",
	"cerebras":     "cerebras",
	"claude":       "anthropic",
	"deepseek":     "deepseek",
	"gemini":       "google",
}

func main() {
	upstreamURL := flag.String("upstream", DefaultUpstream, "upstream catalog URL; with -in, the URL the file was downloaded from")
	in := flag.String("in", "", "read the upstream catalog from this file instead of -upstream")
	overridesPath := flag.String("overrides", "overrides.json", "hand-checked overrides file")
	out := flag.String("out", "models.json", "catalog file to write")
	date := flag.String("date", time.Now().UTC().Format("2006-01-02"), "generated date (YYYY-MM-DD)")
	flag.Parse()
	source := *upstreamURL
	if *in != "" {
		// A local file is recorded as the file unless -upstream says
		// where it was downloaded from.
		source = ""
		flag.Visit(func(f *flag.Flag) {
			if f.Name == "upstream" {
				source = *upstreamURL
			}
		})
	}
	if err := run(source, *in, *overridesPath, *out, *date, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "catalog gen:", err)
		os.Exit(1)
	}
}

// run generates out. upstreamURL is fetched unless in names a local file;
// the catalog records upstreamURL as its source, or "file:<in>" when
// upstreamURL is empty, so a hand-made input never claims models.dev
// provenance.
func run(upstreamURL, in, overridesPath, out, date string, report io.Writer) error {
	raw, err := readUpstream(upstreamURL, in)
	if err != nil {
		return err
	}
	source := upstreamURL
	if in != "" && source == "" {
		source = "file:" + filepath.ToSlash(in)
	}
	var upstream map[string]upstreamProvider
	if err := json.Unmarshal(raw, &upstream); err != nil {
		return fmt.Errorf("parse upstream catalog: %w", err)
	}
	overridesRaw, err := os.ReadFile(overridesPath)
	if err != nil {
		return fmt.Errorf("read overrides: %w", err)
	}
	overrides, err := parseOverrides(overridesRaw)
	if err != nil {
		return err
	}
	file, err := build(upstream, overrides, model.Providers, upstreamIDs, date, source)
	if err != nil {
		return err
	}
	var previous *catalog.File
	if old, err := os.ReadFile(out); err == nil {
		// An unreadable previous file only loses the comparison.
		if parsed, err := catalog.Parse(old); err == nil {
			previous = parsed
		}
	}
	if previous != nil && sameProviders(previous, file) && previous.Upstream == file.Upstream {
		// Unchanged data keeps the previous generated date, so the same
		// upstream input reproduces the file byte for byte.
		file.Generated = previous.Generated
	}
	encoded, err := encode(file)
	if err != nil {
		return err
	}
	writeReport(report, previous, file, model.Providers)
	if previous != nil {
		if old, err := os.ReadFile(out); err == nil && bytes.Equal(old, encoded) {
			fmt.Fprintln(report, "models.json unchanged")
			return nil
		}
	}
	tmp := out + ".tmp"
	if err := os.WriteFile(tmp, encoded, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, out)
}

func readUpstream(url, path string) ([]byte, error) {
	if path != "" {
		return os.ReadFile(path)
	}
	client := &http.Client{Timeout: 60 * time.Second}
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "likha-catalog-gen")
	req.Header.Set("Accept", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch upstream catalog: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch upstream catalog: HTTP %s", resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 64<<20))
}

// Upstream (models.dev api.json) shapes. Numbers decode as float64 so a
// fractional limit never fails the whole file.

type upstreamProvider struct {
	ID     string                   `json:"id"`
	Models map[string]upstreamModel `json:"models"`
}

type upstreamModel struct {
	Name        string `json:"name"`
	ToolCall    bool   `json:"tool_call"`
	Reasoning   bool   `json:"reasoning"`
	Status      string `json:"status"`
	LastUpdated string `json:"last_updated"`
	Modalities  struct {
		Input []string `json:"input"`
	} `json:"modalities"`
	Limit struct {
		Context float64 `json:"context"`
		Input   float64 `json:"input"`
		Output  float64 `json:"output"`
	} `json:"limit"`
	Cost *upstreamCost `json:"cost"`
}

type upstreamRates struct {
	Input      *float64 `json:"input"`
	Output     *float64 `json:"output"`
	CacheRead  *float64 `json:"cache_read"`
	CacheWrite *float64 `json:"cache_write"`
	Reasoning  *float64 `json:"reasoning"`
}

type upstreamCost struct {
	upstreamRates
	Tiers []struct {
		upstreamRates
		Tier struct {
			Type string  `json:"type"`
			Size float64 `json:"size"`
		} `json:"tier"`
	} `json:"tiers"`
	ContextOver200k *upstreamRates `json:"context_over_200k"`
}

// overridesFile is the hand-checked correction layer. Every provider
// billing change and every model override must cite ref and verified.
type overridesFile struct {
	Providers map[string]overrideProvider `json:"providers"`
}

type overrideProvider struct {
	Billing  string                   `json:"billing,omitempty"`
	Ref      string                   `json:"ref,omitempty"`
	Verified string                   `json:"verified,omitempty"`
	Models   map[string]overrideModel `json:"models,omitempty"`
}

// overrideModel sets only the fields it names; Cost replaces the whole
// price card. Drop removes the model.
type overrideModel struct {
	Name       *string       `json:"name,omitempty"`
	Context    *int64        `json:"context,omitempty"`
	Input      *int64        `json:"input,omitempty"`
	Output     *int64        `json:"output,omitempty"`
	Cost       *overrideCost `json:"cost,omitempty"`
	Reasoning  *bool         `json:"reasoning,omitempty"`
	Modalities []string      `json:"modalities,omitempty"`
	Status     *string       `json:"status,omitempty"`
	Drop       bool          `json:"drop,omitempty"`
	Ref        string        `json:"ref"`
	Verified   string        `json:"verified"`
	Note       string        `json:"note,omitempty"`
}

// overrideCost is a whole price card. Input and output are pointers so a
// missing or misspelled rate fails the run instead of pricing at $0.
type overrideCost struct {
	overrideRates
	Tiers []overrideTier `json:"tiers,omitempty"`
}

type overrideTier struct {
	Above int64 `json:"above"`
	overrideRates
}

type overrideRates struct {
	Input      *float64 `json:"input"`
	Output     *float64 `json:"output"`
	CacheRead  *float64 `json:"cache_read,omitempty"`
	CacheWrite *float64 `json:"cache_write,omitempty"`
	Reasoning  *float64 `json:"reasoning,omitempty"`
}

func (r overrideRates) rates() (catalog.Rates, error) {
	if r.Input == nil || r.Output == nil {
		return catalog.Rates{}, fmt.Errorf("cost needs both input and output")
	}
	return catalog.Rates{Input: *r.Input, Output: *r.Output, CacheRead: r.CacheRead, CacheWrite: r.CacheWrite, Reasoning: r.Reasoning}, nil
}

// card converts the override into a catalog price card; the base rates and
// every tier must state both input and output.
func (c overrideCost) card() (*catalog.Cost, error) {
	base, err := c.rates()
	if err != nil {
		return nil, err
	}
	card := &catalog.Cost{Rates: base}
	for _, t := range c.Tiers {
		r, err := t.rates()
		if err != nil {
			return nil, fmt.Errorf("tier above %d: %w", t.Above, err)
		}
		card.Tiers = append(card.Tiers, catalog.Tier{Above: t.Above, Rates: r})
	}
	return card, nil
}

// parseOverrides decodes overrides.json strictly: an unknown or misspelled
// key at any level fails the run rather than being silently ignored.
func parseOverrides(raw []byte) (overridesFile, error) {
	var o overridesFile
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&o); err != nil {
		return overridesFile{}, fmt.Errorf("parse overrides: %w", err)
	}
	if dec.More() {
		return overridesFile{}, fmt.Errorf("parse overrides: trailing data after the object")
	}
	return o, nil
}

// build converts the upstream catalog into Likha's catalog and applies the
// overrides. It never returns a partially valid file.
func build(upstream map[string]upstreamProvider, overrides overridesFile, providers []model.Provider, mapping map[string]string, date, sourceURL string) (*catalog.File, error) {
	if _, err := time.Parse("2006-01-02", date); err != nil {
		return nil, fmt.Errorf("generated date %q: want YYYY-MM-DD", date)
	}
	file := &catalog.File{
		Schema:    catalog.Schema,
		Generated: date,
		Upstream:  catalog.Upstream{Name: catalog.SourceUpstream, URL: sourceURL},
		Providers: map[string]catalog.Provider{},
	}
	known := map[string]bool{}
	for _, p := range providers {
		known[p.Name] = true
		upstreamID, mapped := mapping[p.Name]
		if !mapped {
			return nil, fmt.Errorf("provider %q has no upstream mapping in gen/main.go", p.Name)
		}
		out := catalog.Provider{Upstream: upstreamID, Models: map[string]catalog.Model{}}
		if upstreamID != "" {
			src, ok := upstream[upstreamID]
			if !ok {
				return nil, fmt.Errorf("upstream catalog has no provider %q (for %s)", upstreamID, p.Name)
			}
			for id, um := range src.Models {
				if !um.ToolCall {
					continue // Likha requires tool calls
				}
				m, err := convert(um)
				if err != nil {
					return nil, fmt.Errorf("%s/%s: %w", upstreamID, id, err)
				}
				out.Models[id] = m
			}
		}
		file.Providers[p.Name] = out
	}
	for _, name := range sortedKeys(mapping) {
		if !known[name] {
			return nil, fmt.Errorf("upstream mapping names %q, which is not a Likha provider", name)
		}
	}
	if err := applyOverrides(file, overrides); err != nil {
		return nil, err
	}
	if err := catalog.Validate(file); err != nil {
		return nil, err
	}
	return file, nil
}

func convert(um upstreamModel) (catalog.Model, error) {
	for _, limit := range []float64{um.Limit.Context, um.Limit.Input, um.Limit.Output} {
		if limit < 0 || math.IsNaN(limit) || math.IsInf(limit, 0) {
			return catalog.Model{}, fmt.Errorf("negative or non-finite limit %v", limit)
		}
	}
	m := catalog.Model{
		Name:       strings.TrimSpace(um.Name),
		Context:    toTokens(um.Limit.Context),
		Output:     toTokens(um.Limit.Output),
		Reasoning:  um.Reasoning,
		Modalities: um.Modalities.Input,
		Status:     um.Status,
		Updated:    um.LastUpdated,
		Source:     catalog.SourceUpstream,
	}
	if in := toTokens(um.Limit.Input); in > 0 && (m.Context == 0 || in < m.Context) {
		m.Input = in
	}
	if um.Cost != nil && um.Cost.Input != nil && um.Cost.Output != nil {
		cost := &catalog.Cost{Rates: rates(um.Cost.upstreamRates, catalog.Rates{})}
		for _, t := range um.Cost.Tiers {
			// Only context tiers can be priced; any other kind, or a
			// threshold that is not a positive count, is an upstream change
			// the generator must learn rather than drop.
			if t.Tier.Type != "context" {
				return catalog.Model{}, fmt.Errorf("unsupported price tier type %q", t.Tier.Type)
			}
			if t.Tier.Size <= 0 || math.IsNaN(t.Tier.Size) || math.IsInf(t.Tier.Size, 0) {
				return catalog.Model{}, fmt.Errorf("price tier size %v is not a positive count", t.Tier.Size)
			}
			cost.Tiers = append(cost.Tiers, catalog.Tier{Above: toTokens(t.Tier.Size), Rates: rates(t.upstreamRates, cost.Rates)})
		}
		sort.Slice(cost.Tiers, func(i, j int) bool { return cost.Tiers[i].Above < cost.Tiers[j].Above })
		if over := um.Cost.ContextOver200k; over != nil {
			// Upstream uses context_over_200k as an alias of the first
			// context tier, whatever its threshold (200K, 256K, 272K, 512K
			// in the 2026-10-04 file), so it never sets a threshold itself.
			if len(cost.Tiers) == 0 {
				// The threshold is unknown, so prompts above it cannot be
				// priced: the row stays unpriced until an override states
				// the card.
				m.Cost = nil
				return m, nil
			}
			first := rates(*over, cost.Rates)
			if first.Input != cost.Tiers[0].Input || first.Output != cost.Tiers[0].Output {
				return catalog.Model{}, fmt.Errorf("context_over_200k ($%g/$%g) disagrees with the first context tier ($%g/$%g above %d)",
					first.Input, first.Output, cost.Tiers[0].Input, cost.Tiers[0].Output, cost.Tiers[0].Above)
			}
		}
		m.Cost = cost
	}
	return m, nil
}

// rates converts upstream rates; a tier missing its input or output rate
// inherits the base rate rather than pricing at zero.
func rates(u upstreamRates, base catalog.Rates) catalog.Rates {
	r := catalog.Rates{Input: base.Input, Output: base.Output, CacheRead: u.CacheRead, CacheWrite: u.CacheWrite, Reasoning: u.Reasoning}
	if u.Input != nil {
		r.Input = *u.Input
	}
	if u.Output != nil {
		r.Output = *u.Output
	}
	return r
}

func toTokens(v float64) int64 {
	if v <= 0 || math.IsNaN(v) || math.IsInf(v, 0) {
		return 0
	}
	return int64(math.Round(v))
}

func applyOverrides(file *catalog.File, overrides overridesFile) error {
	for _, providerID := range sortedKeys(overrides.Providers) {
		op := overrides.Providers[providerID]
		p, ok := file.Providers[providerID]
		if !ok {
			return fmt.Errorf("overrides: unknown provider %q", providerID)
		}
		if op.Billing != "" {
			if op.Ref == "" || op.Verified == "" {
				return fmt.Errorf("overrides: %s billing needs ref and verified", providerID)
			}
			p.Billing = op.Billing
		}
		for _, modelID := range sortedKeys(op.Models) {
			om := op.Models[modelID]
			if om.Ref == "" || om.Verified == "" {
				return fmt.Errorf("overrides: %s/%s needs ref and verified", providerID, modelID)
			}
			if _, err := time.Parse("2006-01-02", om.Verified); err != nil {
				return fmt.Errorf("overrides: %s/%s verified %q: want YYYY-MM-DD", providerID, modelID, om.Verified)
			}
			if om.Drop {
				if _, exists := p.Models[modelID]; !exists {
					return fmt.Errorf("overrides: %s/%s drop names a model that is not in the catalog", providerID, modelID)
				}
				delete(p.Models, modelID)
				continue
			}
			m, upstreamRow := p.Models[modelID]
			var set []string
			if om.Name != nil {
				m.Name = *om.Name
				set = append(set, "name")
			}
			if om.Context != nil {
				m.Context = *om.Context
				set = append(set, "context")
			}
			if om.Input != nil {
				m.Input = *om.Input
				set = append(set, "input")
			}
			if om.Output != nil {
				m.Output = *om.Output
				set = append(set, "output")
			}
			if om.Cost != nil {
				cost, err := om.Cost.card()
				if err != nil {
					return fmt.Errorf("overrides: %s/%s: %w", providerID, modelID, err)
				}
				m.Cost = cost
				set = append(set, "cost")
			}
			if om.Reasoning != nil {
				m.Reasoning = *om.Reasoning
				set = append(set, "reasoning")
			}
			if om.Modalities != nil {
				m.Modalities = om.Modalities
				set = append(set, "modalities")
			}
			if om.Status != nil {
				m.Status = *om.Status
				set = append(set, "status")
			}
			if upstreamRow {
				// The row's ref vouches only for the fields it replaced; the
				// rest are still upstream values.
				if len(set) == 0 {
					return fmt.Errorf("overrides: %s/%s changes no field of the upstream row", providerID, modelID)
				}
				m.Overridden = set
			}
			m.Source, m.Ref, m.Verified, m.Note = catalog.SourceOverride, om.Ref, om.Verified, om.Note
			p.Models[modelID] = m
		}
		file.Providers[providerID] = p
	}
	return nil
}

func encode(file *catalog.File) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(file); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func sameProviders(a, b *catalog.File) bool {
	ea, errA := json.Marshal(a.Providers)
	eb, errB := json.Marshal(b.Providers)
	return errA == nil && errB == nil && bytes.Equal(ea, eb)
}

// writeReport prints what a reviewer must look at: counts per provider,
// changed prices and limits, added and removed models, and provider
// defaults the catalog cannot price.
func writeReport(w io.Writer, previous, next *catalog.File, providers []model.Provider) {
	fmt.Fprintf(w, "catalog %s from %s\n", next.Generated, next.Upstream.URL)
	for _, id := range sortedKeys(next.Providers) {
		p := next.Providers[id]
		priced := 0
		for _, m := range p.Models {
			if m.Cost != nil {
				priced++
			}
		}
		line := fmt.Sprintf("  %-13s %4d models, %4d priced", id, len(p.Models), priced)
		if previous != nil {
			line += fmt.Sprintf(" (was %d)", len(previous.Providers[id].Models))
		}
		if p.Billing != "" {
			line += ", billing " + p.Billing
		}
		fmt.Fprintln(w, line)
	}
	if previous != nil {
		var changes []string
		for _, id := range sortedKeys(next.Providers) {
			oldModels, newModels := previous.Providers[id].Models, next.Providers[id].Models
			for _, mid := range sortedKeys(newModels) {
				nm := newModels[mid]
				om, existed := oldModels[mid]
				if !existed {
					changes = append(changes, fmt.Sprintf("  + %s/%s %s", id, mid, describe(nm)))
					continue
				}
				if a, b := describe(om), describe(nm); a != b {
					changes = append(changes, fmt.Sprintf("  ~ %s/%s %s -> %s", id, mid, a, b))
				}
			}
			for _, mid := range sortedKeys(oldModels) {
				if _, kept := newModels[mid]; !kept {
					changes = append(changes, fmt.Sprintf("  - %s/%s", id, mid))
				}
			}
		}
		if len(changes) == 0 {
			fmt.Fprintln(w, "no model changes")
		} else {
			fmt.Fprintf(w, "%d model changes:\n%s\n", len(changes), strings.Join(changes, "\n"))
		}
	}
	for _, gap := range defaultGaps(next, providers) {
		fmt.Fprintln(w, "default without entry:", gap)
	}
}

// describe is the reviewable summary of a row: limits, the whole price card
// (every rate RequestCost reads, every tier threshold and rate), status, and
// provenance, so no priced change can slip past the report.
func describe(m catalog.Model) string {
	price := "price unknown"
	if m.Cost != nil {
		price = describeRates(m.Cost.Rates)
		for _, t := range m.Cost.Tiers {
			price += fmt.Sprintf("; above %d %s", t.Above, describeRates(t.Rates))
		}
	}
	s := fmt.Sprintf("[ctx %d in %d out %d, %s", m.Context, m.Input, m.Output, price)
	if m.Status != "" {
		s += ", " + m.Status
	}
	s += ", " + m.Source
	if len(m.Overridden) > 0 {
		s += " (" + strings.Join(m.Overridden, ",") + ")"
	}
	return s + "]"
}

func describeRates(r catalog.Rates) string {
	s := fmt.Sprintf("$%g/$%g", r.Input, r.Output)
	for _, opt := range []struct {
		label string
		rate  *float64
	}{{"cache", r.CacheRead}, {"write", r.CacheWrite}, {"reasoning", r.Reasoning}} {
		if opt.rate != nil {
			s += fmt.Sprintf(" %s $%g", opt.label, *opt.rate)
		}
	}
	return s
}

// defaultGaps lists provider/model pairs whose DefaultModel has no row.
func defaultGaps(file *catalog.File, providers []model.Provider) []string {
	var gaps []string
	for _, p := range providers {
		if p.DefaultModel == "" {
			continue
		}
		if _, ok := file.Lookup(p.Name, p.DefaultModel); !ok {
			gaps = append(gaps, p.Name+"/"+p.DefaultModel)
		}
	}
	return gaps
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
