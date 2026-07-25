package tui

import (
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/viewport"
	"github.com/charmbracelet/lipgloss"

	"github.com/CaptShanks/terraprism/internal/tfplan"
)

var ansiPattern = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func stripRenderANSI(s string) string {
	return ansiPattern.ReplaceAllString(s, "")
}

// leaf builds a scalar attribute for tests. Path is left empty; use
// withPaths to fill in Path for a whole tree before rendering.
func leaf(name string, action tfplan.Action, kind tfplan.ValueKind, old, new any) tfplan.Attribute {
	return tfplan.Attribute{Name: name, Kind: kind, Action: action, Old: old, New: new}
}

// mapBlock builds a map-kind container attribute for tests.
func mapBlock(name string, action tfplan.Action, children ...tfplan.Attribute) tfplan.Attribute {
	return tfplan.Attribute{Name: name, Kind: tfplan.KindMap, Action: action, Children: children}
}

// withPaths recursively fills in Path (dotted, matching tfplan's real
// convert.go convention) for a hand-built attribute tree, so tests can
// construct trees by nesting literals without computing paths by hand.
func withPaths(attrs []tfplan.Attribute, parent string) []tfplan.Attribute {
	out := make([]tfplan.Attribute, len(attrs))
	for i, a := range attrs {
		path := a.Name
		if parent != "" {
			path = parent + "." + a.Name
		}
		a.Path = path
		a.Children = withPaths(a.Children, path)
		out[i] = a
	}
	return out
}

// renderResourceForTest renders a resource's attribute tree the way
// Model.renderAttributeTree does when the resource is expanded, with a
// fresh Model (no cursor/fold overrides beyond what the caller sets on r).
func renderResourceForTest(r tfplan.Resource, diffContext int) string {
	m := Model{
		viewport:     viewport.New(120, 40),
		foldedBlocks: make(map[string]bool),
		blockCursor:  -1,
		diffContext:  diffContext,
	}
	var b strings.Builder
	foldIdx := 0
	lineCount := 0
	m.renderAttributeTree(&b, r.Address, r.Attributes, 0, true, false, &foldIdx, &lineCount)
	return stripRenderANSI(b.String())
}

// Terraform's JSON plan carries the resource's full before/after state,
// so a partially-changed resource has mostly-unchanged attributes mixed
// in with the real diff. Rendering every one of them individually — even
// muted — buried the actual change, so unchanged runs collapse into a
// single "# (N unchanged attributes hidden)" note instead, matching
// Terraform CLI's own convention.
func TestUnchangedAttributesCollapseIntoHiddenCountNote(t *testing.T) {
	r := tfplan.Resource{
		Address: "kubectl_manifest.example",
		Action:  tfplan.ActionUpdate,
		Attributes: withPaths([]tfplan.Attribute{
			leaf("api_version", tfplan.ActionNoOp, tfplan.KindString, "v1", "v1"),
			leaf("force_conflicts", tfplan.ActionNoOp, tfplan.KindBool, false, false),
			leaf("live_uid", tfplan.ActionNoOp, tfplan.KindString, "abc-123", "abc-123"),
			leaf("wait_for_rollout", tfplan.ActionUpdate, tfplan.KindBool, false, true),
			leaf("timeouts", tfplan.ActionNoOp, tfplan.KindNull, nil, nil),
		}, ""),
	}

	got := renderResourceForTest(r, 0)

	if !strings.Contains(got, "# (3 unchanged attributes hidden)") {
		t.Fatalf("expected a single hidden-count note for the leading run of 3 unchanged attributes:\n%s", got)
	}
	if strings.Contains(got, "api_version") || strings.Contains(got, "force_conflicts") || strings.Contains(got, "live_uid") {
		t.Fatalf("unchanged attribute names should not be rendered individually:\n%s", got)
	}
	if !strings.Contains(got, "wait_for_rollout") {
		t.Fatalf("expected the actually-changed attribute to still render:\n%s", got)
	}
	if !strings.Contains(got, "# (1 unchanged attribute hidden)") {
		t.Fatalf("expected a singular-noun hidden-count note for the trailing run of 1:\n%s", got)
	}
}

func TestRenderGenericLargeBlockCollapsesByDefault(t *testing.T) {
	children := make([]tfplan.Attribute, 0, 35)
	for i := 0; i < 35; i++ {
		children = append(children, leaf("key"+string(rune('a'+i%26)), tfplan.ActionNoOp, tfplan.KindString, "value", "value"))
	}
	metadata := mapBlock("metadata", tfplan.ActionUpdate, children...)
	values := leaf("values", tfplan.ActionUpdate, tfplan.KindString,
		"controller:\n  replicaCount: 2\n",
		"controller:\n  replicaCount: 3\n",
	)

	r := tfplan.Resource{
		Address:    "helm_release.chart",
		Type:       "helm_release",
		Action:     tfplan.ActionUpdate,
		Attributes: withPaths([]tfplan.Attribute{metadata, values}, ""),
	}

	got := renderResourceForTest(r, 0)

	if !strings.Contains(got, "▶ ~ metadata = { ... 35 attrs }") {
		t.Fatalf("expected metadata block to collapse by default:\n%s", got)
	}
	if strings.Contains(got, "keya") {
		t.Fatalf("collapsed block content should be hidden:\n%s", got)
	}
	if !strings.Contains(got, "replicaCount: 2") || !strings.Contains(got, "replicaCount: 3") {
		t.Fatalf("expected multiline string diff to show both sides:\n%s", got)
	}
}

// A changed multi-line string is structurally a single Attribute with one
// Old and one New value — there's no "pairing" step needed the way the old
// heredoc-marker text parser needed to pair a removed block with an added
// one, so it always renders as exactly one diff block.
func TestMultilineStringRendersAsSingleDiffBlock(t *testing.T) {
	r := tfplan.Resource{
		Address: "helm_release.chart",
		Action:  tfplan.ActionUpdate,
		Attributes: withPaths([]tfplan.Attribute{
			leaf("values", tfplan.ActionUpdate, tfplan.KindString,
				"controller:\n  replicaCount: 2\n",
				"controller:\n  replicaCount: 3\n"),
		}, ""),
	}

	got := renderResourceForTest(r, 0)
	if count := strings.Count(got, "<<EOT"); count != 1 {
		t.Fatalf("expected exactly one multiline diff block, got %d:\n%s", count, got)
	}
	if !strings.Contains(got, "replicaCount: 2") || !strings.Contains(got, "replicaCount: 3") {
		t.Fatalf("expected both old and new content visible:\n%s", got)
	}
}

// Large multi-line string diffs must be foldable, the same way large
// containers are: this regressed when multiline rendering was first
// introduced (always fully expanded, no collapse indicator at all).
func TestLargeMultilineStringDiffIsFoldable(t *testing.T) {
	var oldLines, newLines []string
	for i := 0; i < 40; i++ {
		oldLines = append(oldLines, fmt.Sprintf("line-%d: unchanged", i))
		newLines = append(newLines, fmt.Sprintf("line-%d: unchanged", i))
	}
	newLines[20] = "line-20: changed"
	attr := leaf("values", tfplan.ActionUpdate, tfplan.KindString,
		strings.Join(oldLines, "\n"), strings.Join(newLines, "\n"))

	r := tfplan.Resource{
		Address:    "helm_release.chart",
		Action:     tfplan.ActionUpdate,
		Attributes: withPaths([]tfplan.Attribute{attr}, ""),
	}

	// Collapsed by default (40 lines exceeds defaultCollapsedFoldLines).
	got := renderResourceForTest(r, 0)
	if !strings.Contains(got, "▶ ~ values = <<EOT ... 40 lines") {
		t.Fatalf("expected a collapsed fold header with a line-count summary:\n%s", got)
	}
	if strings.Contains(got, "line-0: unchanged") {
		t.Fatalf("collapsed multiline content should be hidden:\n%s", got)
	}

	// Expanding it (via blockCursor + toggle) reveals the content and
	// flips the indicator, exactly like a container fold.
	m := Model{
		plan:         &tfplan.Plan{Resources: []tfplan.Resource{r}},
		expanded:     map[int]bool{0: true},
		foldedBlocks: make(map[string]bool),
		blockCursor:  0,
		viewport:     viewport.New(120, 40),
	}
	if !m.setCurrentFoldCollapsed(false) {
		t.Fatal("expected the multiline attribute to be a navigable fold block")
	}

	var b strings.Builder
	foldIdx := 0
	lineCount := 0
	m.renderAttributeTree(&b, r.Address, r.Attributes, 0, true, false, &foldIdx, &lineCount)
	expanded := stripRenderANSI(b.String())

	if !strings.Contains(expanded, "▼ ~ values = <<EOT") {
		t.Fatalf("expected an expanded fold header after toggling:\n%s", expanded)
	}
	if !strings.Contains(expanded, "line-20: unchanged") || !strings.Contains(expanded, "line-20: changed") {
		t.Fatalf("expanded multiline content should show the diffed line:\n%s", expanded)
	}
}

func TestDiffContextControlsMultilineContextLines(t *testing.T) {
	oldVal := strings.Join([]string{
		"before-a: true", "before-b: true", "before-c: true",
		"target: old",
		"after-a: true", "after-b: true", "after-c: true",
	}, "\n")
	newVal := strings.Join([]string{
		"before-a: true", "before-b: true", "before-c: true",
		"target: new",
		"after-a: true", "after-b: true", "after-c: true",
	}, "\n")

	r := tfplan.Resource{
		Address:    "helm_release.chart",
		Action:     tfplan.ActionUpdate,
		Attributes: withPaths([]tfplan.Attribute{leaf("values", tfplan.ActionUpdate, tfplan.KindString, oldVal, newVal)}, ""),
	}

	withoutContext := renderResourceForTest(r, 0)
	if strings.Contains(withoutContext, "before-a: true") || strings.Contains(withoutContext, "after-c: true") {
		t.Fatalf("expected zero diff context to hide far context lines:\n%s", withoutContext)
	}
	if !strings.Contains(withoutContext, "target: old") || !strings.Contains(withoutContext, "target: new") {
		t.Fatalf("expected changed lines to remain visible with zero context:\n%s", withoutContext)
	}

	withContext := renderResourceForTest(r, 3)
	for _, want := range []string{"before-a: true", "before-b: true", "before-c: true", "after-a: true", "after-b: true", "after-c: true"} {
		if !strings.Contains(withContext, want) {
			t.Fatalf("expected expanded diff context to include %q:\n%s", want, withContext)
		}
	}
}

func TestDiffContextHotkeysClampContext(t *testing.T) {
	m := Model{
		plan:        &tfplan.Plan{},
		viewport:    viewport.New(80, 20),
		diffContext: defaultDiffContext,
	}

	m, _, handled := handleKeyIncreaseDiffContext(m)
	if !handled {
		t.Fatal("expected increase diff context key to be handled")
	}
	if got, want := m.diffContextSize(), defaultDiffContext+diffContextStep; got != want {
		t.Fatalf("diff context after increase = %d, want %d", got, want)
	}

	for i := 0; i < 20; i++ {
		m, _, _ = handleKeyIncreaseDiffContext(m)
	}
	if got := m.diffContextSize(); got != maxDiffContext {
		t.Fatalf("diff context should clamp to max %d, got %d", maxDiffContext, got)
	}

	for i := 0; i < 20; i++ {
		m, _, _ = handleKeyDecreaseDiffContext(m)
	}
	if got := m.diffContextSize(); got != 0 {
		t.Fatalf("diff context should clamp to 0, got %d", got)
	}
}

// nestedMetadataResource builds a resource with a two-level-deep fold
// tree: metadata { values { nested = true } }.
func nestedMetadataResource() tfplan.Resource {
	nested := leaf("nested", tfplan.ActionUpdate, tfplan.KindBool, false, true)
	values := mapBlock("values", tfplan.ActionUpdate, nested)
	metadata := mapBlock("metadata", tfplan.ActionUpdate, values)
	return tfplan.Resource{
		Address:    "helm_release.chart",
		Action:     tfplan.ActionUpdate,
		Attributes: withPaths([]tfplan.Attribute{metadata}, ""),
	}
}

func TestVisibleFoldBlocksExcludesChildrenOfCollapsedParent(t *testing.T) {
	r := nestedMetadataResource()
	m := Model{
		plan:         &tfplan.Plan{Resources: []tfplan.Resource{r}},
		expanded:     map[int]bool{0: true},
		foldedBlocks: make(map[string]bool),
		blockCursor:  -1,
	}
	blocks := allFoldableAttributes(r.Address, r.Attributes, 0)
	if len(blocks) != 2 {
		t.Fatalf("expected parent and child folds, got %d", len(blocks))
	}

	m.foldedBlocks[blocks[0].Key] = true
	visible := m.currentFoldBlocks()
	if len(visible) != 1 {
		t.Fatalf("expected only collapsed parent to be visible, got %d", len(visible))
	}
	if visible[0].Key != blocks[0].Key {
		t.Fatalf("expected visible fold to be parent, got %q", visible[0].Key)
	}
}

func TestVisibleFoldBlocksIncludesChildrenOfExpandedParent(t *testing.T) {
	r := nestedMetadataResource()
	m := Model{
		plan:         &tfplan.Plan{Resources: []tfplan.Resource{r}},
		expanded:     map[int]bool{0: true},
		foldedBlocks: make(map[string]bool),
		blockCursor:  -1,
	}

	visible := m.currentFoldBlocks()
	if len(visible) != 2 {
		t.Fatalf("expected parent and child folds to be visible, got %d", len(visible))
	}
	if visible[0].Path != "metadata" || visible[1].Path != "metadata.values" {
		t.Fatalf("unexpected visible fold order: %#v", visible)
	}
}

func TestSetCurrentScopeFoldsCollapsedResourceScope(t *testing.T) {
	r := nestedMetadataResource()
	m := Model{
		plan:         &tfplan.Plan{Resources: []tfplan.Resource{r}},
		expanded:     map[int]bool{0: true},
		foldedBlocks: make(map[string]bool),
		blockCursor:  -1,
	}

	if !m.setCurrentScopeFoldsCollapsed(true) {
		t.Fatal("expected resource-scope collapse to apply")
	}
	for _, block := range allFoldableAttributes(r.Address, r.Attributes, 0) {
		if !m.foldedBlocks[block.Key] {
			t.Fatalf("expected fold %q to be collapsed", block.Key)
		}
	}

	if !m.setCurrentScopeFoldsCollapsed(false) {
		t.Fatal("expected resource-scope expand to apply")
	}
	for _, block := range allFoldableAttributes(r.Address, r.Attributes, 0) {
		if m.foldedBlocks[block.Key] {
			t.Fatalf("expected fold %q to be expanded", block.Key)
		}
	}
}

func TestSetCurrentScopeFoldsCollapsedSubBlockScope(t *testing.T) {
	nested := leaf("nested", tfplan.ActionUpdate, tfplan.KindBool, false, true)
	values := mapBlock("values", tfplan.ActionUpdate, nested)
	metadata := mapBlock("metadata", tfplan.ActionUpdate, values)
	set := mapBlock("set", tfplan.ActionUpdate, leaf("value", tfplan.ActionUpdate, tfplan.KindBool, false, true))
	r := tfplan.Resource{
		Address:    "helm_release.chart",
		Action:     tfplan.ActionUpdate,
		Attributes: withPaths([]tfplan.Attribute{metadata, set}, ""),
	}
	m := Model{
		plan:         &tfplan.Plan{Resources: []tfplan.Resource{r}},
		expanded:     map[int]bool{0: true},
		foldedBlocks: make(map[string]bool),
		blockCursor:  0,
	}
	blocks := allFoldableAttributes(r.Address, r.Attributes, 0)
	if len(blocks) != 3 {
		t.Fatalf("expected metadata, values, and set folds, got %#v", blocks)
	}

	if !m.setCurrentScopeFoldsCollapsed(true) {
		t.Fatal("expected sub-block-scope collapse to apply")
	}
	if !m.foldedBlocks[blocks[0].Key] || !m.foldedBlocks[blocks[1].Key] {
		t.Fatalf("expected selected fold and descendant to collapse: %#v", m.foldedBlocks)
	}
	if m.foldedBlocks[blocks[2].Key] {
		t.Fatalf("did not expect sibling fold to collapse: %#v", m.foldedBlocks)
	}
}

func TestExpandAndCollapseEverythingAffectsAllDisplayedResourcesAndFolds(t *testing.T) {
	metadata := mapBlock("metadata", tfplan.ActionUpdate,
		mapBlock("values", tfplan.ActionUpdate, leaf("nested", tfplan.ActionUpdate, tfplan.KindBool, false, true)))
	spec := mapBlock("spec", tfplan.ActionUpdate, leaf("replicas", tfplan.ActionUpdate, tfplan.KindNumber, "2", "3"))

	resources := []tfplan.Resource{
		{
			Address:    "helm_release.chart",
			Action:     tfplan.ActionUpdate,
			Attributes: withPaths([]tfplan.Attribute{metadata}, ""),
		},
		{
			Address:    "kubectl_manifest.vmagent",
			Action:     tfplan.ActionUpdate,
			Attributes: withPaths([]tfplan.Attribute{spec}, ""),
		},
	}
	m := Model{
		plan:         &tfplan.Plan{Resources: resources},
		expanded:     map[int]bool{0: false, 1: false},
		foldedBlocks: make(map[string]bool),
		blockCursor:  1,
	}

	m.expandEverything()
	for idx := range resources {
		if !m.expanded[idx] {
			t.Fatalf("expected resource %d to be expanded", idx)
		}
		for _, block := range allFoldableAttributes(resources[idx].Address, resources[idx].Attributes, 0) {
			if m.foldedBlocks[block.Key] {
				t.Fatalf("expected fold %q to be expanded", block.Key)
			}
		}
	}
	if m.blockCursor != -1 {
		t.Fatalf("expected block cursor to reset after global expand, got %d", m.blockCursor)
	}

	m.collapseEverything()
	for idx := range resources {
		if m.expanded[idx] {
			t.Fatalf("expected resource %d to be collapsed", idx)
		}
		for _, block := range allFoldableAttributes(resources[idx].Address, resources[idx].Attributes, 0) {
			if !m.foldedBlocks[block.Key] {
				t.Fatalf("expected fold %q to be collapsed", block.Key)
			}
		}
	}
}

func TestViewHelpFooterUsesCompactTextForNarrowWidths(t *testing.T) {
	m := Model{width: 72}
	got := m.viewHelpFooter()
	if lipgloss.Width(got) > 68 {
		t.Fatalf("help footer width = %d, want <= 68: %q", lipgloss.Width(got), got)
	}
	if strings.Contains(got, "expand/collapse scope") {
		t.Fatalf("expected compact help footer, got %q", got)
	}
}
