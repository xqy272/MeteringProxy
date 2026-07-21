package pricing

import (
	"bytes"
	"fmt"
	"io"
	"math"
	"os"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// Supported per-image output resolution keys.
const (
	ImageSizeDefault = "default"
	ImageSize1K      = "1K"
	ImageSize2K      = "2K"
)

type Pricing struct {
	Models     map[string]ModelPrice           `yaml:"pricing"`
	Multimodal map[string]MultimodalModelPrice `yaml:"multimodal_pricing"`
	// aliasIndex maps alias names to their canonical model key.
	aliasIndex           map[string]string
	multimodalAliasIndex map[string]string
	// yamlPresence is populated only by Parse/Load. Programmatic NewPricing
	// configs leave it empty so Validate does not require YAML field presence.
	yamlPresence yamlPresence
}

type ModelPrice struct {
	InputPer1M         float64           `yaml:"input_per_1m"`
	CachedInputPer1M   float64           `yaml:"cached_input_per_1m"`
	OutputPer1M        float64           `yaml:"output_per_1m"`
	ReasoningPer1M     float64           `yaml:"reasoning_per_1m"`
	CacheCreationPer1M float64           `yaml:"cache_creation_per_1m"`
	Aliases            []string          `yaml:"aliases"`
	LongContext        *LongContextPrice `yaml:"long_context"`
}

// LongContextPrice is an optional second text pricing tier selected from a
// single-request input token threshold. It is intentionally model-agnostic.
type LongContextPrice struct {
	ThresholdInputTokens int64   `yaml:"threshold_input_tokens"`
	InputPer1M           float64 `yaml:"input_per_1m"`
	CachedInputPer1M     float64 `yaml:"cached_input_per_1m"`
	OutputPer1M          float64 `yaml:"output_per_1m"`
	ReasoningPer1M       float64 `yaml:"reasoning_per_1m"`
	CacheCreationPer1M   float64 `yaml:"cache_creation_per_1m"`
}

type MultimodalModelPrice struct {
	Text         ModalityPrice `yaml:"text"`
	Image        ModalityPrice `yaml:"image"`
	Audio        ModalityPrice `yaml:"audio"`
	AudioSeconds ModalityPrice `yaml:"audio_seconds"`
	Aliases      []string      `yaml:"aliases"`
}

type ModalityPrice struct {
	InputPer1M       float64            `yaml:"input_per_1m"`
	CachedInputPer1M float64            `yaml:"cached_input_per_1m"`
	OutputPer1M      float64            `yaml:"output_per_1m"`
	ReasoningPer1M   float64            `yaml:"reasoning_per_1m"`
	PerSecond        float64            `yaml:"per_second"`
	InputPerSecond   float64            `yaml:"input_per_second"`
	OutputPerSecond  float64            `yaml:"output_per_second"`
	PerMinute        float64            `yaml:"per_minute"`
	InputPerMinute   float64            `yaml:"input_per_minute"`
	OutputPerMinute  float64            `yaml:"output_per_minute"`
	PerImageInput    float64            `yaml:"per_image_input"`
	PerImageOutput   map[string]float64 `yaml:"per_image_output"`
	// hasPerImageInput is true when YAML explicitly set per_image_input, or when
	// a programmatic caller set a non-zero PerImageInput.
	hasPerImageInput bool
	// perImageOutputKeySet is true when YAML included the per_image_output key
	// (including null / empty mapping).
	perImageOutputKeySet bool
}

// TextTokenUsage is the typed text pricing input.
//
// Token fields are the amounts to bill for a homogeneous bucket.
// RequestInputTokens is the single-request input token value used only for
// long-context tier selection. Callers must not pass an aggregated sum across
// mixed short/long requests as RequestInputTokens.
type TextTokenUsage struct {
	InputTokens         int64
	OutputTokens        int64
	ReasoningTokens     int64
	CachedTokens        int64
	CacheCreationTokens int64
	RequestInputTokens  int64
}

type yamlPresence struct {
	fromYAML   bool
	models     map[string]modelPresence
	multimodal map[string]multimodalPresence
}

type modelPresence struct {
	inputPer1M  bool
	outputPer1M bool
	longContext *longContextPresence // non-nil when long_context key is present
}

type longContextPresence struct {
	thresholdInputTokens bool
	inputPer1M           bool
	outputPer1M          bool
}

type multimodalPresence struct {
	text         modalityPresence
	image        modalityPresence
	audio        modalityPresence
	audioSeconds modalityPresence
}

type modalityPresence struct {
	perImageInput        bool
	perImageOutputKeySet bool
}

// NewPricing returns an empty Pricing with initialized index.
func NewPricing() *Pricing {
	return &Pricing{
		Models:               make(map[string]ModelPrice),
		Multimodal:           make(map[string]MultimodalModelPrice),
		aliasIndex:           make(map[string]string),
		multimodalAliasIndex: make(map[string]string),
	}
}

// Load reads, strictly decodes, validates, and indexes a pricing YAML file.
func Load(path string) (*Pricing, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read pricing: %w", err)
	}
	return Parse(data)
}

// Parse strictly decodes pricing YAML bytes, validates, and builds alias indexes.
func Parse(data []byte) (*Pricing, error) {
	p := NewPricing()
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(p); err != nil {
		if err != io.EOF {
			return nil, fmt.Errorf("parse pricing: %w", err)
		}
	}
	var extra yaml.Node
	if err := dec.Decode(&extra); err != io.EOF {
		if err == nil {
			return nil, fmt.Errorf("parse pricing: multiple YAML documents are not allowed")
		}
		return nil, fmt.Errorf("parse pricing: %w", err)
	}
	if p.Models == nil {
		p.Models = make(map[string]ModelPrice)
	}
	if p.Multimodal == nil {
		p.Multimodal = make(map[string]MultimodalModelPrice)
	}

	// Empty document is a usable empty config.
	if len(bytes.TrimSpace(data)) == 0 {
		if err := p.Validate(); err != nil {
			return nil, err
		}
		p.buildAliasIndex()
		return p, nil
	}

	var root yaml.Node
	if err := yaml.Unmarshal(data, &root); err != nil {
		return nil, fmt.Errorf("parse pricing: %w", err)
	}
	presence, err := collectYAMLPresence(&root)
	if err != nil {
		return nil, err
	}
	p.yamlPresence = presence
	applyYAMLPresence(p)

	if err := p.validateYAMLRequiredFields(); err != nil {
		return nil, err
	}
	if err := p.Validate(); err != nil {
		return nil, err
	}
	p.buildAliasIndex()
	return p, nil
}

func applyYAMLPresence(p *Pricing) {
	for name, mp := range p.Multimodal {
		pr, ok := p.yamlPresence.multimodal[name]
		if !ok {
			continue
		}
		mp.Text.hasPerImageInput = pr.text.perImageInput || mp.Text.PerImageInput != 0
		mp.Text.perImageOutputKeySet = pr.text.perImageOutputKeySet
		mp.Image.hasPerImageInput = pr.image.perImageInput || mp.Image.PerImageInput != 0
		mp.Image.perImageOutputKeySet = pr.image.perImageOutputKeySet
		mp.Audio.hasPerImageInput = pr.audio.perImageInput || mp.Audio.PerImageInput != 0
		mp.Audio.perImageOutputKeySet = pr.audio.perImageOutputKeySet
		mp.AudioSeconds.hasPerImageInput = pr.audioSeconds.perImageInput || mp.AudioSeconds.PerImageInput != 0
		mp.AudioSeconds.perImageOutputKeySet = pr.audioSeconds.perImageOutputKeySet
		p.Multimodal[name] = mp
	}
}

// Validate checks pricing semantics. Errors include stable YAML paths and never
// include credentials or secret material.
//
// Programmatic configs from NewPricing remain valid without required YAML field
// presence. Parse enforces required-field presence separately.
func (p *Pricing) Validate() error {
	if p == nil {
		return fmt.Errorf("pricing: nil config")
	}
	if p.Models == nil {
		p.Models = make(map[string]ModelPrice)
	}
	if p.Multimodal == nil {
		p.Multimodal = make(map[string]MultimodalModelPrice)
	}

	for _, name := range sortedKeys(p.Models) {
		mp := p.Models[name]
		base := "pricing." + yamlMapKey(name)
		if err := validateFiniteNonNegativePrice(base+".input_per_1m", mp.InputPer1M); err != nil {
			return err
		}
		if err := validateFiniteNonNegativePrice(base+".cached_input_per_1m", mp.CachedInputPer1M); err != nil {
			return err
		}
		if err := validateFiniteNonNegativePrice(base+".output_per_1m", mp.OutputPer1M); err != nil {
			return err
		}
		if err := validateFiniteNonNegativePrice(base+".reasoning_per_1m", mp.ReasoningPer1M); err != nil {
			return err
		}
		if err := validateFiniteNonNegativePrice(base+".cache_creation_per_1m", mp.CacheCreationPer1M); err != nil {
			return err
		}
		if err := validateAliases(base+".aliases", mp.Aliases); err != nil {
			return err
		}
		if mp.LongContext != nil {
			lcBase := base + ".long_context"
			if mp.LongContext.ThresholdInputTokens <= 0 {
				return fmt.Errorf("%s.threshold_input_tokens: must be a positive integer", lcBase)
			}
			if err := validateFiniteNonNegativePrice(lcBase+".input_per_1m", mp.LongContext.InputPer1M); err != nil {
				return err
			}
			if err := validateFiniteNonNegativePrice(lcBase+".output_per_1m", mp.LongContext.OutputPer1M); err != nil {
				return err
			}
			if err := validateFiniteNonNegativePrice(lcBase+".cached_input_per_1m", mp.LongContext.CachedInputPer1M); err != nil {
				return err
			}
			if err := validateFiniteNonNegativePrice(lcBase+".reasoning_per_1m", mp.LongContext.ReasoningPer1M); err != nil {
				return err
			}
			if err := validateFiniteNonNegativePrice(lcBase+".cache_creation_per_1m", mp.LongContext.CacheCreationPer1M); err != nil {
				return err
			}
		}
	}

	for _, name := range sortedKeys(p.Multimodal) {
		mp := p.Multimodal[name]
		base := "multimodal_pricing." + yamlMapKey(name)
		if err := validateAliases(base+".aliases", mp.Aliases); err != nil {
			return err
		}
		if err := validateModalityPrice(base+".text", mp.Text); err != nil {
			return err
		}
		if err := validateModalityPrice(base+".image", mp.Image); err != nil {
			return err
		}
		if err := validateModalityPrice(base+".audio", mp.Audio); err != nil {
			return err
		}
		if err := validateModalityPrice(base+".audio_seconds", mp.AudioSeconds); err != nil {
			return err
		}
	}

	if err := validateTextAliasNamespace(p); err != nil {
		return err
	}
	if err := validateMultimodalAliasNamespace(p); err != nil {
		return err
	}
	return nil
}

func (p *Pricing) validateYAMLRequiredFields() error {
	if !p.yamlPresence.fromYAML {
		return nil
	}
	for _, name := range sortedKeys(p.Models) {
		base := "pricing." + yamlMapKey(name)
		pr := p.yamlPresence.models[name]
		if !pr.inputPer1M {
			return fmt.Errorf("%s.input_per_1m: required field is missing", base)
		}
		if !pr.outputPer1M {
			return fmt.Errorf("%s.output_per_1m: required field is missing", base)
		}
		// long_context key present in YAML (including explicit null) requires a
		// mapping with threshold/input/output. Gate on YAML presence, not decode pointer.
		if pr.longContext != nil {
			lcBase := base + ".long_context"
			lpr := *pr.longContext
			if !lpr.thresholdInputTokens {
				return fmt.Errorf("%s.threshold_input_tokens: required field is missing", lcBase)
			}
			if !lpr.inputPer1M {
				return fmt.Errorf("%s.input_per_1m: required field is missing", lcBase)
			}
			if !lpr.outputPer1M {
				return fmt.Errorf("%s.output_per_1m: required field is missing", lcBase)
			}
			if p.Models[name].LongContext == nil {
				return fmt.Errorf("%s: must be a mapping", lcBase)
			}
		}
	}
	return nil
}

func collectYAMLPresence(root *yaml.Node) (yamlPresence, error) {
	out := yamlPresence{
		fromYAML:   true,
		models:     make(map[string]modelPresence),
		multimodal: make(map[string]multimodalPresence),
	}
	if root == nil {
		return out, nil
	}
	node := root
	if node.Kind == yaml.DocumentNode {
		if len(node.Content) == 0 {
			return out, nil
		}
		node = node.Content[0]
	}
	if node.Kind == yaml.ScalarNode && (node.Tag == "!!null" || node.Value == "" || node.Value == "null") {
		return out, nil
	}
	if node.Kind != yaml.MappingNode {
		return out, nil
	}
	for i := 0; i+1 < len(node.Content); i += 2 {
		key := node.Content[i].Value
		val := node.Content[i+1]
		switch key {
		case "pricing":
			if err := collectModelPresence(val, out.models); err != nil {
				return yamlPresence{}, err
			}
		case "multimodal_pricing":
			if err := collectMultimodalPresence(val, out.multimodal); err != nil {
				return yamlPresence{}, err
			}
		}
	}
	return out, nil
}

func collectModelPresence(node *yaml.Node, out map[string]modelPresence) error {
	if node == nil || node.Kind == yaml.ScalarNode && (node.Tag == "!!null" || node.Value == "null") {
		return nil
	}
	if node.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(node.Content); i += 2 {
		name := node.Content[i].Value
		body := node.Content[i+1]
		pr := modelPresence{}
		if body != nil && body.Kind == yaml.MappingNode {
			for j := 0; j+1 < len(body.Content); j += 2 {
				field := body.Content[j].Value
				child := body.Content[j+1]
				switch field {
				case "input_per_1m":
					pr.inputPer1M = true
				case "output_per_1m":
					pr.outputPer1M = true
				case "long_context":
					lpr := &longContextPresence{}
					if child != nil && child.Kind == yaml.MappingNode {
						for k := 0; k+1 < len(child.Content); k += 2 {
							switch child.Content[k].Value {
							case "threshold_input_tokens":
								lpr.thresholdInputTokens = true
							case "input_per_1m":
								lpr.inputPer1M = true
							case "output_per_1m":
								lpr.outputPer1M = true
							}
						}
					}
					pr.longContext = lpr
				}
			}
		}
		out[name] = pr
	}
	return nil
}

func collectMultimodalPresence(node *yaml.Node, out map[string]multimodalPresence) error {
	if node == nil || node.Kind == yaml.ScalarNode && (node.Tag == "!!null" || node.Value == "null") {
		return nil
	}
	if node.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(node.Content); i += 2 {
		name := node.Content[i].Value
		body := node.Content[i+1]
		pr := multimodalPresence{}
		if body != nil && body.Kind == yaml.MappingNode {
			for j := 0; j+1 < len(body.Content); j += 2 {
				field := body.Content[j].Value
				child := body.Content[j+1]
				switch field {
				case "text":
					pr.text = collectModalityPresence(child)
				case "image":
					pr.image = collectModalityPresence(child)
				case "audio":
					pr.audio = collectModalityPresence(child)
				case "audio_seconds":
					pr.audioSeconds = collectModalityPresence(child)
				}
			}
		}
		out[name] = pr
	}
	return nil
}

func collectModalityPresence(node *yaml.Node) modalityPresence {
	var pr modalityPresence
	if node == nil || node.Kind != yaml.MappingNode {
		return pr
	}
	for i := 0; i+1 < len(node.Content); i += 2 {
		switch node.Content[i].Value {
		case "per_image_input":
			pr.perImageInput = true
		case "per_image_output":
			pr.perImageOutputKeySet = true
		}
	}
	return pr
}

func validateTextAliasNamespace(p *Pricing) error {
	canonical := make(map[string]struct{}, len(p.Models))
	for name := range p.Models {
		canonical[name] = struct{}{}
	}
	seen := make(map[string]string)
	for _, name := range sortedKeys(p.Models) {
		base := "pricing." + yamlMapKey(name) + ".aliases"
		for i, alias := range p.Models[name].Aliases {
			alias = strings.TrimSpace(alias)
			path := fmt.Sprintf("%s[%d]", base, i)
			if alias == "" {
				return fmt.Errorf("%s: alias must not be empty", path)
			}
			if _, ok := canonical[alias]; ok {
				return fmt.Errorf("%s: alias %q conflicts with canonical model %q", path, alias, alias)
			}
			if owner, ok := seen[alias]; ok {
				return fmt.Errorf("%s: duplicate alias %q (also defined under pricing.%s)", path, alias, yamlMapKey(owner))
			}
			seen[alias] = name
		}
	}
	return nil
}

func validateMultimodalAliasNamespace(p *Pricing) error {
	canonical := make(map[string]struct{}, len(p.Multimodal))
	for name := range p.Multimodal {
		canonical[name] = struct{}{}
	}
	seen := make(map[string]string)
	for _, name := range sortedKeys(p.Multimodal) {
		base := "multimodal_pricing." + yamlMapKey(name) + ".aliases"
		for i, alias := range p.Multimodal[name].Aliases {
			alias = strings.TrimSpace(alias)
			path := fmt.Sprintf("%s[%d]", base, i)
			if alias == "" {
				return fmt.Errorf("%s: alias must not be empty", path)
			}
			if _, ok := canonical[alias]; ok {
				return fmt.Errorf("%s: alias %q conflicts with canonical model %q", path, alias, alias)
			}
			if owner, ok := seen[alias]; ok {
				return fmt.Errorf("%s: duplicate alias %q (also defined under multimodal_pricing.%s)", path, alias, yamlMapKey(owner))
			}
			seen[alias] = name
		}
	}
	return nil
}

func validateAliases(base string, aliases []string) error {
	seen := make(map[string]struct{}, len(aliases))
	for i, alias := range aliases {
		alias = strings.TrimSpace(alias)
		path := fmt.Sprintf("%s[%d]", base, i)
		if alias == "" {
			return fmt.Errorf("%s: alias must not be empty", path)
		}
		if _, ok := seen[alias]; ok {
			return fmt.Errorf("%s: duplicate alias %q within the same model", path, alias)
		}
		seen[alias] = struct{}{}
	}
	return nil
}

func validateModalityPrice(base string, mp ModalityPrice) error {
	if err := validateFiniteNonNegativePrice(base+".input_per_1m", mp.InputPer1M); err != nil {
		return err
	}
	if err := validateFiniteNonNegativePrice(base+".cached_input_per_1m", mp.CachedInputPer1M); err != nil {
		return err
	}
	if err := validateFiniteNonNegativePrice(base+".output_per_1m", mp.OutputPer1M); err != nil {
		return err
	}
	if err := validateFiniteNonNegativePrice(base+".reasoning_per_1m", mp.ReasoningPer1M); err != nil {
		return err
	}
	if err := validateFiniteNonNegativePrice(base+".per_second", mp.PerSecond); err != nil {
		return err
	}
	if err := validateFiniteNonNegativePrice(base+".input_per_second", mp.InputPerSecond); err != nil {
		return err
	}
	if err := validateFiniteNonNegativePrice(base+".output_per_second", mp.OutputPerSecond); err != nil {
		return err
	}
	if err := validateFiniteNonNegativePrice(base+".per_minute", mp.PerMinute); err != nil {
		return err
	}
	if err := validateFiniteNonNegativePrice(base+".input_per_minute", mp.InputPerMinute); err != nil {
		return err
	}
	if err := validateFiniteNonNegativePrice(base+".output_per_minute", mp.OutputPerMinute); err != nil {
		return err
	}
	if err := validateFiniteNonNegativePrice(base+".per_image_input", mp.PerImageInput); err != nil {
		return err
	}

	// Explicit null/empty per_image_output is "set but invalid".
	if mp.perImageOutputKeySet {
		if mp.PerImageOutput == nil || len(mp.PerImageOutput) == 0 {
			return fmt.Errorf("%s.per_image_output: must include default when set", base)
		}
	}
	if mp.PerImageOutput == nil {
		return nil
	}
	if len(mp.PerImageOutput) == 0 {
		return fmt.Errorf("%s.per_image_output: must include default when set", base)
	}
	if _, ok := mp.PerImageOutput[ImageSizeDefault]; !ok {
		return fmt.Errorf("%s.per_image_output: missing required key %q", base, ImageSizeDefault)
	}
	keys := make([]string, 0, len(mp.PerImageOutput))
	for k := range mp.PerImageOutput {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if !isSupportedImageSizeKey(k) {
			return fmt.Errorf("%s.per_image_output.%s: unsupported resolution key %q (allowed: default, 1K, 2K)", base, yamlMapKey(k), k)
		}
		if err := validateFiniteNonNegativePrice(base+".per_image_output."+yamlMapKey(k), mp.PerImageOutput[k]); err != nil {
			return err
		}
	}
	return nil
}

func isSupportedImageSizeKey(k string) bool {
	switch k {
	case ImageSizeDefault, ImageSize1K, ImageSize2K:
		return true
	default:
		return false
	}
}

func validateFiniteNonNegativePrice(path string, v float64) error {
	if math.IsNaN(v) {
		return fmt.Errorf("%s: price must not be NaN", path)
	}
	if math.IsInf(v, 0) {
		return fmt.Errorf("%s: price must not be infinite", path)
	}
	if v < 0 {
		return fmt.Errorf("%s: price must not be negative", path)
	}
	return nil
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func yamlMapKey(k string) string {
	if k == "" {
		return `""`
	}
	if strings.ContainsAny(k, ".[] \t\"'") {
		return strconv.Quote(k)
	}
	return k
}

func (p *Pricing) buildAliasIndex() {
	p.aliasIndex = make(map[string]string)
	for name, mp := range p.Models {
		for _, alias := range mp.Aliases {
			alias = strings.TrimSpace(alias)
			if alias != "" {
				p.aliasIndex[alias] = name
			}
		}
	}
	p.multimodalAliasIndex = make(map[string]string)
	for name, mp := range p.Multimodal {
		for _, alias := range mp.Aliases {
			alias = strings.TrimSpace(alias)
			if alias != "" {
				p.multimodalAliasIndex[alias] = name
			}
		}
	}
}

// Cost computes the cost for a given model using base (short-tier) prices only.
// Long-context tiers are intentionally ignored here so aggregate handlers cannot
// misclassify summed request inputs. Use CostText for tier-aware pricing.
//
// The matching chain is:
//  1. exact model name match
//  2. explicit alias match (configured in pricing.yaml)
//  3. canonicalized model name match
//  4. unknown (cost = 0, known = false)
//
// Canonicalization strips known provider date tags (e.g., "-2026-03-17").
// It is only used for pricing lookup and never alters the persisted model name.
func (p *Pricing) Cost(model string, inputTokens, outputTokens, reasoningTokens, cachedTokens int64) (float64, bool) {
	return p.CostWithCacheCreation(model, inputTokens, outputTokens, reasoningTokens, cachedTokens, 0)
}

func (p *Pricing) CostWithCacheCreation(model string, inputTokens, outputTokens, reasoningTokens, cachedTokens, cacheCreationTokens int64) (float64, bool) {
	mp, ok := p.lookup(model)
	if !ok {
		return 0, false
	}
	// Base-price only: do not apply long_context from aggregate inputs.
	return computeCost(mp, inputTokens, outputTokens, reasoningTokens, cachedTokens, cacheCreationTokens), true
}

// CostText computes text cost with optional long-context tier selection.
// Tier selection uses RequestInputTokens only; billed amounts use the other fields.
func (p *Pricing) CostText(model string, usage TextTokenUsage) (float64, bool) {
	mp, ok := p.lookup(model)
	if !ok {
		return 0, false
	}
	resolved := resolveTextTier(mp, usage.RequestInputTokens)
	return computeCost(resolved, usage.InputTokens, usage.OutputTokens, usage.ReasoningTokens, usage.CachedTokens, usage.CacheCreationTokens), true
}

// resolveTextTier returns the effective base ModelPrice for the request tier.
//
// Long-tier optional fields follow computeCost semantics within the long tier:
//   - missing long reasoning_per_1m (0) => charge reasoning at long output_per_1m
//   - missing long cache_creation_per_1m (0) => charge cache creation at long input_per_1m
//   - long cached_input_per_1m 0 remains free (no short-tier inheritance)
func resolveTextTier(mp ModelPrice, requestInputTokens int64) ModelPrice {
	if mp.LongContext == nil || requestInputTokens < mp.LongContext.ThresholdInputTokens {
		out := mp
		out.LongContext = nil
		out.Aliases = nil
		return out
	}
	lc := mp.LongContext
	return ModelPrice{
		InputPer1M:         lc.InputPer1M,
		CachedInputPer1M:   lc.CachedInputPer1M,
		OutputPer1M:        lc.OutputPer1M,
		ReasoningPer1M:     lc.ReasoningPer1M,
		CacheCreationPer1M: lc.CacheCreationPer1M,
	}
}

func (p *Pricing) CostDimension(model, modality, channel, metric, direction, unit string, amount float64) (float64, bool) {
	if amount <= 0 {
		return 0, true
	}
	mp, ok := p.lookupMultimodal(model)
	if !ok {
		return 0, false
	}
	if metric == "tokens" && unit == "token" {
		price, ok := modalityTokenPrice(mp, modality, channel)
		if !ok {
			return 0, false
		}
		rate, ok := tokenDirectionRate(price, direction)
		if !ok || rate <= 0 {
			return 0, false
		}
		return amount / 1_000_000.0 * rate, true
	}
	if metric == "seconds" && unit == "second" {
		price := mp.AudioSeconds
		rate := secondsRate(price.PerSecond, price.PerMinute)
		switch direction {
		case "input":
			if inputRate := secondsRate(price.InputPerSecond, price.InputPerMinute); inputRate > 0 {
				rate = inputRate
			}
		case "output":
			if outputRate := secondsRate(price.OutputPerSecond, price.OutputPerMinute); outputRate > 0 {
				rate = outputRate
			}
		}
		if rate <= 0 {
			return 0, false
		}
		return amount * rate, true
	}
	return 0, false
}

// NormalizeImageSize maps a raw size string to a supported per-image key.
// defaulted is true when the input was empty or unrecognized.
func NormalizeImageSize(size string) (normalized string, defaulted bool) {
	s := strings.TrimSpace(size)
	if s == "" {
		return ImageSizeDefault, true
	}
	switch {
	case strings.EqualFold(s, ImageSizeDefault):
		return ImageSizeDefault, false
	case strings.EqualFold(s, ImageSize1K) || strings.HasPrefix(s, "1024"):
		return ImageSize1K, false
	case strings.EqualFold(s, ImageSize2K) || strings.HasPrefix(s, "2048"):
		return ImageSize2K, false
	default:
		return ImageSizeDefault, true
	}
}

// CostImages computes per-image input and output costs for a multimodal model.
//
// count <= 0 contributes no charge on that side.
// If both counts are <= 0 there is no billable dimension: cost=0, known=true,
// without requiring a model match.
// If a positive count needs a missing price, known is false, but any already
// known side subtotal is still returned.
// This API never invents missing output image counts.
func (p *Pricing) CostImages(model string, inputImageCount, outputImageCount int64, size string) (cost float64, known bool, sizeDefaulted bool) {
	_, defaulted := NormalizeImageSize(size)
	if inputImageCount <= 0 && outputImageCount <= 0 {
		return 0, true, defaulted
	}

	normalized, defaulted := NormalizeImageSize(size)
	mp, ok := p.lookupMultimodal(model)
	if !ok {
		return 0, false, defaulted
	}

	total := 0.0
	known = true

	if inputImageCount > 0 {
		if !perImageInputConfigured(mp.Image) {
			known = false
		} else {
			total += float64(inputImageCount) * mp.Image.PerImageInput
		}
	}
	if outputImageCount > 0 {
		if mp.Image.PerImageOutput == nil {
			known = false
		} else if rate, ok := mp.Image.PerImageOutput[normalized]; !ok {
			known = false
		} else {
			total += float64(outputImageCount) * rate
		}
	}
	return total, known, defaulted
}

func perImageInputConfigured(mp ModalityPrice) bool {
	if mp.hasPerImageInput {
		return true
	}
	// Programmatic non-zero values are treated as configured free/paid rates.
	return mp.PerImageInput != 0
}

func secondsRate(perSecond, perMinute float64) float64 {
	if perSecond > 0 {
		return perSecond
	}
	if perMinute > 0 {
		return perMinute / 60
	}
	return 0
}

func (p *Pricing) lookup(model string) (ModelPrice, bool) {
	// 1. Exact match.
	if mp, ok := p.Models[model]; ok {
		return mp, true
	}

	// 2. Explicit alias match.
	if canonical, ok := p.aliasIndex[model]; ok {
		if mp, ok := p.Models[canonical]; ok {
			return mp, true
		}
	}

	// 3. Canonicalized form match.
	canonical := canonicalize(model)
	if canonical != model {
		if mp, ok := p.Models[canonical]; ok {
			return mp, true
		}
	}

	return ModelPrice{}, false
}

// HasMultimodal reports whether model resolves to an entry in multimodal_pricing
// via exact name, configured alias, or date-suffix canonicalization.
// It does not inspect token rates or image counts.
func (p *Pricing) HasMultimodal(model string) bool {
	_, ok := p.lookupMultimodal(model)
	return ok
}

// HasPerImagePricing reports whether model explicitly enables a per-image
// billing channel. A multimodal model may be token-priced only; image counts in
// that case are metadata and must not be treated as a missing per-image rate.
func (p *Pricing) HasPerImagePricing(model string) bool {
	mp, ok := p.lookupMultimodal(model)
	if !ok {
		return false
	}
	return perImageInputConfigured(mp.Image) || mp.Image.perImageOutputKeySet || mp.Image.PerImageOutput != nil
}

// HasImageTokenPricing reports whether a multimodal model enables any text or
// image token rate used by image requests. It lets reports distinguish a
// per-image-only model from a token-priced model whose usage dimensions are
// unexpectedly missing.
func (p *Pricing) HasImageTokenPricing(model string) bool {
	mp, ok := p.lookupMultimodal(model)
	if !ok {
		return false
	}
	return modalityHasTokenPrice(mp.Text) || modalityHasTokenPrice(mp.Image)
}

func modalityHasTokenPrice(mp ModalityPrice) bool {
	return mp.InputPer1M > 0 || mp.CachedInputPer1M > 0 || mp.OutputPer1M > 0 || mp.ReasoningPer1M > 0
}

func (p *Pricing) lookupMultimodal(model string) (MultimodalModelPrice, bool) {
	if mp, ok := p.Multimodal[model]; ok {
		return mp, true
	}
	if canonical, ok := p.multimodalAliasIndex[model]; ok {
		if mp, ok := p.Multimodal[canonical]; ok {
			return mp, true
		}
	}
	canonical := canonicalize(model)
	if canonical != model {
		if mp, ok := p.Multimodal[canonical]; ok {
			return mp, true
		}
	}
	return MultimodalModelPrice{}, false
}

func modalityTokenPrice(mp MultimodalModelPrice, modality, channel string) (ModalityPrice, bool) {
	switch channel {
	case "text":
		return mp.Text, true
	case "image":
		return mp.Image, true
	case "audio":
		return mp.Audio, true
	}
	switch modality {
	case "text":
		return mp.Text, true
	case "image":
		return mp.Image, true
	case "audio":
		return mp.Audio, true
	}
	return ModalityPrice{}, false
}

func tokenDirectionRate(price ModalityPrice, direction string) (float64, bool) {
	switch direction {
	case "input":
		return price.InputPer1M, true
	case "cached_input":
		return price.CachedInputPer1M, true
	case "output":
		return price.OutputPer1M, true
	case "reasoning":
		return price.ReasoningPer1M, true
	}
	return 0, false
}

// canonicalize strips provider-specific version suffixes from model names.
// This is only used for pricing lookup. The stored model_returned is never altered.
//
// Rules:
//   - OpenAI style: "gpt-4o-2026-03-18" -> "gpt-4o" (strip trailing -YYYY-MM-DD)
//   - Anthropic style: "claude-sonnet-4-6-20250514" -> "claude-sonnet-4-6" (strip trailing -YYYYMMDD)
//   - DeepSeek style: "deepseek-chat-2026-05-01" -> "deepseek-chat" (strip trailing -YYYY-MM-DD)
func canonicalize(model string) string {
	// Strip trailing -YYYY-MM-DD (OpenAI, DeepSeek style: 10 chars including leading dash).
	if len(model) > 11 {
		suffix := model[len(model)-11:] // "-2026-03-18" = 11 chars
		if isDateSuffix(suffix) {
			return model[:len(model)-11]
		}
	}

	// Strip trailing -YYYYMMDD (Anthropic style: 9 chars including leading dash).
	if len(model) > 9 {
		suffix := model[len(model)-9:] // "-20250514" = 9 chars
		if isCompactDateSuffix(suffix) {
			return model[:len(model)-9]
		}
	}

	return model
}

// isDateSuffix checks if a string matches "-YYYY-MM-DD" format.
func isDateSuffix(s string) bool {
	if len(s) != 11 || s[0] != '-' {
		return false
	}
	for i := 1; i < 11; i++ {
		c := s[i]
		if i == 5 || i == 8 {
			if c != '-' {
				return false
			}
			continue
		}
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// isCompactDateSuffix checks if a string matches "-YYYYMMDD" format.
func isCompactDateSuffix(s string) bool {
	if len(s) != 9 || s[0] != '-' {
		return false
	}
	for i := 1; i < 9; i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

func computeCost(mp ModelPrice, inputTokens, outputTokens, reasoningTokens, cachedTokens, cacheCreationTokens int64) float64 {
	cost := 0.0

	nonCachedInput, cachedTokens, cacheCreationTokens := inputBreakdown(inputTokens, cachedTokens, cacheCreationTokens)

	if cachedTokens > 0 {
		cost += float64(cachedTokens) / 1_000_000.0 * mp.CachedInputPer1M
	}
	if nonCachedInput > 0 {
		cost += float64(nonCachedInput) / 1_000_000.0 * mp.InputPer1M
	}
	if cacheCreationTokens > 0 && mp.CacheCreationPer1M > 0 {
		cost += float64(cacheCreationTokens) / 1_000_000.0 * mp.CacheCreationPer1M
	} else if cacheCreationTokens > 0 {
		cost += float64(cacheCreationTokens) / 1_000_000.0 * mp.InputPer1M
	}

	if reasoningTokens > 0 && mp.ReasoningPer1M > 0 {
		regularOutput := outputTokens - reasoningTokens
		if regularOutput > 0 {
			cost += float64(regularOutput) / 1_000_000.0 * mp.OutputPer1M
		}
		cost += float64(reasoningTokens) / 1_000_000.0 * mp.ReasoningPer1M
	} else {
		cost += float64(outputTokens) / 1_000_000.0 * mp.OutputPer1M
	}

	return cost
}

func inputBreakdown(inputTokens, cachedTokens, cacheCreationTokens int64) (nonCachedInput, cachedInput, cacheCreationInput int64) {
	if cachedTokens < 0 {
		cachedTokens = 0
	}
	if cacheCreationTokens < 0 {
		cacheCreationTokens = 0
	}
	if inputTokens <= 0 {
		return 0, cachedTokens, cacheCreationTokens
	}
	if cacheCreationTokens > inputTokens {
		cacheCreationTokens = inputTokens
	}
	remaining := inputTokens - cacheCreationTokens
	if cachedTokens > remaining {
		cachedTokens = remaining
	}
	return remaining - cachedTokens, cachedTokens, cacheCreationTokens
}
