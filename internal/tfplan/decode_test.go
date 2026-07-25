package tfplan

// testdata/plan_real_capture.json and testdata/plan_opentofu_capture.json
// were captured from real `terraform`/`tofu` runs against no-network
// providers (null_resource/random_id), to confirm hand-written fixtures
// haven't drifted from what the engines actually emit. Regenerate with:
//
//	terraform init && terraform plan -out=plan.bin && terraform show -json plan.bin
//	tofu init && tofu plan -out=plan.bin && tofu show -json plan.bin

import (
	"os"
	"strings"
	"testing"
)

func loadFixture(t *testing.T, name string) []byte {
	t.Helper()
	raw, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatalf("reading fixture %s: %v", name, err)
	}
	return raw
}

func TestDecode(t *testing.T) {
	tests := []struct {
		name    string
		fixture string
		wantErr bool
		check   func(t *testing.T, p *Plan)
	}{
		{
			name:    "create simple",
			fixture: "plan_create_simple.json",
			check: func(t *testing.T, p *Plan) {
				if len(p.Resources) != 1 {
					t.Fatalf("got %d resources, want 1", len(p.Resources))
				}
				r := p.Resources[0]
				if r.Action != ActionCreate {
					t.Errorf("action = %s, want create", r.Action)
				}
				if p.TotalAdd != 1 || p.TotalChange != 0 || p.TotalDestroy != 0 {
					t.Errorf("totals = add:%d change:%d destroy:%d, want 1/0/0", p.TotalAdd, p.TotalChange, p.TotalDestroy)
				}
				if p.OutputCount != 1 {
					t.Errorf("OutputCount = %d, want 1", p.OutputCount)
				}
				// Regression: the output's own attribute Action must stay
				// the real create/update/delete value (here: create, since
				// before is absent and after is unknown) rather than being
				// overwritten with ActionOutput, or renderLeafValue would
				// always show a confusing "old → new" arrow instead of
				// "(known after apply)".
				oc := p.OutputChanges[0]
				if oc.Action != ActionCreate {
					t.Errorf("output attribute Action = %s, want create", oc.Action)
				}
				if !oc.Computed {
					t.Errorf("output attribute should be Computed (after_unknown)")
				}
			},
		},
		{
			name:    "no changes",
			fixture: "plan_no_changes.json",
			check: func(t *testing.T, p *Plan) {
				if len(p.Resources) != 0 {
					t.Errorf("got %d resources, want 0 (no-op filtered out)", len(p.Resources))
				}
				if len(p.OutputChanges) != 0 {
					t.Errorf("got %d output changes, want 0", len(p.OutputChanges))
				}
			},
		},
		{
			name:    "replace patterns",
			fixture: "plan_replace.json",
			check: func(t *testing.T, p *Plan) {
				if len(p.Resources) != 2 {
					t.Fatalf("got %d resources, want 2", len(p.Resources))
				}
				byAddr := map[string]Resource{}
				for _, r := range p.Resources {
					byAddr[r.Address] = r
				}
				dbc := byAddr["aws_instance.destroy_before_create"]
				if dbc.Action != ActionReplace || dbc.ReplacePattern != ReplaceDestroyBeforeCreate {
					t.Errorf("destroy_before_create: action=%s pattern=%s", dbc.Action, dbc.ReplacePattern)
				}
				if len(dbc.ActionReasons) != 1 || dbc.ActionReasons[0] != "requested" {
					t.Errorf("destroy_before_create: ActionReasons = %v, want [requested]", dbc.ActionReasons)
				}
				cbd := byAddr["aws_instance.create_before_destroy"]
				if cbd.Action != ActionReplace || cbd.ReplacePattern != ReplaceCreateBeforeDestroy {
					t.Errorf("create_before_destroy: action=%s pattern=%s", cbd.Action, cbd.ReplacePattern)
				}
				if p.TotalAdd != 2 || p.TotalDestroy != 2 || p.TotalChange != 0 {
					t.Errorf("totals = add:%d change:%d destroy:%d, want 2/0/2", p.TotalAdd, p.TotalChange, p.TotalDestroy)
				}
			},
		},
		{
			name:    "sensitive nested path",
			fixture: "plan_sensitive_nested.json",
			check: func(t *testing.T, p *Plan) {
				if len(p.Resources) != 1 {
					t.Fatalf("got %d resources, want 1", len(p.Resources))
				}
				tags := findAttr(t, p.Resources[0].Attributes, "tags")
				if tags.Sensitive {
					t.Errorf("tags container should not be marked sensitive itself, only its Name child")
				}
				name := findAttr(t, tags.Children, "Name")
				if !name.Sensitive {
					t.Errorf("tags.Name should be sensitive")
				}
				if name.Old != nil || name.New != nil {
					t.Errorf("sensitive leaf should have redacted Old/New, got Old=%v New=%v", name.Old, name.New)
				}
				env := findAttr(t, tags.Children, "Environment")
				if env.Sensitive {
					t.Errorf("tags.Environment should not be sensitive")
				}
				if env.New != "staging" {
					t.Errorf("tags.Environment.New = %v, want staging", env.New)
				}
			},
		},
		{
			name:    "multiline string value (real capture)",
			fixture: "plan_real_capture.json",
			check: func(t *testing.T, p *Plan) {
				var res *Resource
				for i := range p.Resources {
					if p.Resources[i].Address == "null_resource.example" {
						res = &p.Resources[i]
					}
				}
				if res == nil {
					t.Fatalf("null_resource.example not found")
				}
				triggers := findAttr(t, res.Attributes, "triggers")
				values := findAttr(t, triggers.Children, "values_yaml")
				s, ok := values.New.(string)
				if !ok {
					t.Fatalf("values_yaml.New is %T, want string", values.New)
				}
				if !strings.Contains(s, "\n") {
					t.Errorf("values_yaml should be a multi-line string, got %q", s)
				}
				if !strings.Contains(s, "replicaCount") {
					t.Errorf("values_yaml missing expected content: %q", s)
				}
			},
		},
		{
			// Regression test: an attribute explicitly set to JSON null on a
			// newly-created resource must still be classified as ActionCreate,
			// not ActionNoOp. Both "genuinely absent" and "present with value
			// null" decode to Go nil, so the fix must track key/index
			// existence explicitly rather than comparing decoded values to nil.
			name:    "null-valued attribute on create is still Create, not NoOp (real capture)",
			fixture: "plan_real_capture.json",
			check: func(t *testing.T, p *Plan) {
				var res *Resource
				for i := range p.Resources {
					if p.Resources[i].Address == "random_id.example" {
						res = &p.Resources[i]
					}
				}
				if res == nil {
					t.Fatalf("random_id.example not found")
				}
				if res.Action != ActionCreate {
					t.Fatalf("random_id.example action = %s, want create", res.Action)
				}
				keepers := findAttr(t, res.Attributes, "keepers")
				if keepers.New != nil {
					t.Errorf("keepers.New = %v, want nil (explicit JSON null)", keepers.New)
				}
				if keepers.Action != ActionCreate {
					t.Errorf("keepers.Action = %s, want create (was misclassified as no-op before the existence-tracking fix)", keepers.Action)
				}
			},
		},
		{
			name:    "opentofu capture decodes cleanly",
			fixture: "plan_opentofu_capture.json",
			check: func(t *testing.T, p *Plan) {
				if len(p.Resources) != 1 {
					t.Fatalf("got %d resources, want 1", len(p.Resources))
				}
				if p.Resources[0].Action != ActionCreate {
					t.Errorf("action = %s, want create", p.Resources[0].Action)
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			raw := loadFixture(t, tc.fixture)
			p, err := DecodeBytes(raw)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("DecodeBytes: %v", err)
			}
			if tc.check != nil {
				tc.check(t, p)
			}
		})
	}
}

// A plan with output changes but no resource changes (e.g. only a
// computed local or data source value feeding an output) must still
// count as "has changes" — this is what DisplayResources is for.
func TestDisplayResourcesIncludesOutputChanges(t *testing.T) {
	raw := loadFixture(t, "plan_create_simple.json")
	p, err := DecodeBytes(raw)
	if err != nil {
		t.Fatalf("DecodeBytes: %v", err)
	}
	if len(p.OutputChanges) == 0 {
		t.Fatalf("fixture should have at least one output change")
	}

	display := p.DisplayResources()
	if len(display) != len(p.Resources)+len(p.OutputChanges) {
		t.Fatalf("DisplayResources len = %d, want %d resources + %d outputs",
			len(display), len(p.Resources), len(p.OutputChanges))
	}

	outputRes := display[len(display)-1]
	if outputRes.Action != ActionOutput {
		t.Errorf("synthetic output resource Action = %s, want output", outputRes.Action)
	}
	if outputRes.Address != "output.instance_id" {
		t.Errorf("synthetic output resource Address = %q, want output.instance_id", outputRes.Address)
	}
	if len(outputRes.Attributes) != 1 || outputRes.Attributes[0].Name != "value" {
		t.Errorf("scalar output should wrap its value in a single 'value' attribute, got %+v", outputRes.Attributes)
	}
	// The Resource itself is labeled ActionOutput for filtering/display,
	// but the wrapped attribute's own Action must stay the real diffed
	// value (create here), not be overwritten to ActionOutput too.
	if outputRes.Attributes[0].Action != ActionCreate {
		t.Errorf("wrapped output attribute Action = %s, want create", outputRes.Attributes[0].Action)
	}
}

func TestDisplayResourcesNoOutputsReturnsResourcesUnchanged(t *testing.T) {
	p := &Plan{Resources: []Resource{{Address: "a.b"}}}
	display := p.DisplayResources()
	if len(display) != 1 || display[0].Address != "a.b" {
		t.Fatalf("DisplayResources with no outputs = %+v, want unchanged Resources", display)
	}
}

func findAttr(t *testing.T, attrs []Attribute, name string) Attribute {
	t.Helper()
	for _, a := range attrs {
		if a.Name == name {
			return a
		}
	}
	t.Fatalf("attribute %q not found among %d attributes", name, len(attrs))
	return Attribute{}
}

func TestFormatVersionCompatibility(t *testing.T) {
	tests := []struct {
		name          string
		formatVersion string
		wantErr       bool
	}{
		{name: "1.0 accepted", formatVersion: "1.0", wantErr: false},
		{name: "1.2 accepted", formatVersion: "1.2", wantErr: false},
		{name: "0.1 accepted (lower bound)", formatVersion: "0.1", wantErr: false},
		{name: "2.0 rejected (upper bound)", formatVersion: "2.0", wantErr: true},
		{name: "malformed rejected", formatVersion: "not-a-version", wantErr: true},
		{name: "missing rejected", formatVersion: "", wantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			raw := []byte(`{"format_version":"` + tc.formatVersion + `","resource_changes":[],"output_changes":{}}`)
			_, err := DecodeBytes(raw)
			if tc.wantErr && err == nil {
				t.Fatalf("expected error for format_version %q, got nil", tc.formatVersion)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("unexpected error for format_version %q: %v", tc.formatVersion, err)
			}
		})
	}
}
