package tfplan

import (
	"fmt"
	"reflect"
	"sort"
)

// buildAttribute walks before/after and their parallel after_unknown/
// before_sensitive/after_sensitive trees together to build one pre-diffed
// Attribute node for the given path. beforeExists/afterExists track
// whether this path was actually present on each side — distinct from
// before/after being nil, since a JSON `null` value decodes to the same
// Go nil as a genuinely absent key. Without that distinction, an
// attribute explicitly set to null on a newly-created resource (e.g.
// `keepers = null` on a fresh random_id) would be misread as unchanged
// instead of created. All five value inputs are already navigated to the
// same path; buildAttribute recurses to build Children for container
// values.
//
// before_sensitive/after_sensitive/after_unknown share the same shape:
// at any given path the value is either the bool true (meaning "this
// entire subtree is sensitive/unknown, stop here") or a container mirror
// of before/after with only the affected child paths present (missing
// paths default to false). This function assumes exactly that shape.
func buildAttribute(name, path string, before, after any, beforeExists, afterExists bool, afterUnknown, beforeSensitive, afterSensitive any) Attribute {
	sensitiveHere := isTrue(beforeSensitive) || isTrue(afterSensitive)
	unknownHere := isTrue(afterUnknown)
	kind := pickKind(before, after)

	if sensitiveHere {
		// Whole subtree redacted: don't expose values or recurse into
		// children, matching Terraform CLI's "(sensitive value)" display.
		return Attribute{
			Name:      name,
			Path:      path,
			Kind:      kind,
			Action:    diffAction(before, after, beforeExists, afterExists, unknownHere),
			Computed:  unknownHere,
			Sensitive: true,
		}
	}

	switch kind {
	case KindMap:
		return buildMapAttribute(name, path, before, after, beforeExists, afterExists, afterUnknown, beforeSensitive, afterSensitive, unknownHere)
	case KindList:
		return buildListAttribute(name, path, before, after, beforeExists, afterExists, afterUnknown, beforeSensitive, afterSensitive, unknownHere)
	default:
		return Attribute{
			Name:     name,
			Path:     path,
			Kind:     kind,
			Action:   diffAction(before, after, beforeExists, afterExists, unknownHere),
			Old:      before,
			New:      after,
			Computed: unknownHere,
		}
	}
}

func buildMapAttribute(name, path string, before, after any, beforeExists, afterExists bool, afterUnknown, beforeSensitive, afterSensitive any, unknownHere bool) Attribute {
	beforeMap := asMap(before)
	afterMap := asMap(after)
	unknownMap := asMap(afterUnknown)
	beforeSensMap := asMap(beforeSensitive)
	afterSensMap := asMap(afterSensitive)

	keys := unionKeys(beforeMap, afterMap)
	children := make([]Attribute, 0, len(keys))
	for _, k := range keys {
		childPath := path + "." + k
		bv, bExists := beforeMap[k]
		av, aExists := afterMap[k]
		childUnknown := any(unknownMap[k])
		if unknownHere {
			// afterUnknown for this container was the bare bool true, not a
			// per-child map -- "this entire subtree is unknown, stop here"
			// (see buildAttribute's doc comment) -- so there's no per-child
			// unknown entry to consult. Every child inherits unknown from
			// the container.
			childUnknown = true
		}
		children = append(children, buildAttribute(k, childPath, bv, av, bExists, aExists,
			childUnknown, beforeSensMap[k], afterSensMap[k]))
	}

	return Attribute{
		Name:     name,
		Path:     path,
		Kind:     KindMap,
		Action:   aggregateAction(children, beforeExists, afterExists, unknownHere),
		Children: children,
		Computed: unknownHere,
	}
}

func buildListAttribute(name, path string, before, after any, beforeExists, afterExists bool, afterUnknown, beforeSensitive, afterSensitive any, unknownHere bool) Attribute {
	beforeList := asList(before)
	afterList := asList(after)
	unknownList := asList(afterUnknown)
	beforeSensList := asList(beforeSensitive)
	afterSensList := asList(afterSensitive)

	n := len(beforeList)
	if len(afterList) > n {
		n = len(afterList)
	}

	children := make([]Attribute, 0, n)
	for i := 0; i < n; i++ {
		childPath := fmt.Sprintf("%s[%d]", path, i)
		var b, a, u, bs, as any
		bExists := i < len(beforeList)
		aExists := i < len(afterList)
		if bExists {
			b = beforeList[i]
		}
		if aExists {
			a = afterList[i]
		}
		if i < len(unknownList) {
			u = unknownList[i]
		}
		if i < len(beforeSensList) {
			bs = beforeSensList[i]
		}
		if i < len(afterSensList) {
			as = afterSensList[i]
		}
		if unknownHere {
			// See the identical branch in buildMapAttribute: afterUnknown
			// was the bare bool true for this whole list, so there's no
			// per-element unknown entry to consult. Every element inherits
			// unknown from the list.
			u = true
		}
		children = append(children, buildAttribute(fmt.Sprintf("%d", i), childPath, b, a, bExists, aExists, u, bs, as))
	}

	return Attribute{
		Name:     name,
		Path:     path,
		Kind:     KindList,
		Action:   aggregateAction(children, beforeExists, afterExists, unknownHere),
		Children: children,
		Computed: unknownHere,
	}
}

// diffAction derives a leaf node's action from presence/absence on each
// side (not nil-ness of the decoded value, which can't distinguish an
// explicit JSON null from a genuinely absent key) and, when present on
// both sides, direct value comparison. Existence is checked before the
// unknown flag for the create case: a newly-created attribute whose
// value happens to be unknown is still a create (rendered as "(known
// after apply)" via the separate Computed flag), not an "update" showing
// a confusing "null → (known after apply)" arrow. But an attribute that
// existed before and has become wholly unknown is still an update, even
// though Terraform's plan JSON omits it from "after" entirely (the same
// encoding it uses for "genuinely deleted") -- unknown is checked before
// treating a missing "after" as a delete, specifically for that case.
func diffAction(before, after any, beforeExists, afterExists, unknown bool) Action {
	switch {
	case !beforeExists && !afterExists:
		return ActionNoOp
	case !beforeExists && afterExists:
		return ActionCreate
	case beforeExists && !afterExists && unknown:
		return ActionUpdate
	case beforeExists && !afterExists:
		return ActionDelete
	case unknown:
		return ActionUpdate
	case reflect.DeepEqual(before, after):
		return ActionNoOp
	default:
		return ActionUpdate
	}
}

// aggregateAction derives a container node's action from its own
// presence/absence, the unknown flag, and whether any child changed — in
// that priority order, for the same reason as diffAction.
func aggregateAction(children []Attribute, beforeExists, afterExists bool, unknown bool) Action {
	switch {
	case !beforeExists && !afterExists:
		return ActionNoOp
	case !beforeExists && afterExists:
		return ActionCreate
	case beforeExists && !afterExists && unknown:
		return ActionUpdate
	case beforeExists && !afterExists:
		return ActionDelete
	case unknown:
		return ActionUpdate
	}
	for _, c := range children {
		if c.Action != ActionNoOp {
			return ActionUpdate
		}
	}
	return ActionNoOp
}

func isTrue(v any) bool {
	b, ok := v.(bool)
	return ok && b
}

func pickKind(before, after any) ValueKind {
	if after != nil {
		return valueKind(after)
	}
	if before != nil {
		return valueKind(before)
	}
	return KindNull
}

func valueKind(v any) ValueKind {
	switch v.(type) {
	case nil:
		return KindNull
	case string:
		return KindString
	case bool:
		return KindBool
	case []interface{}:
		return KindList
	case map[string]interface{}:
		return KindMap
	default:
		// json.Number and any other scalar decoded by encoding/json.
		return KindNumber
	}
}

func asMap(v any) map[string]interface{} {
	m, _ := v.(map[string]interface{})
	return m
}

func asList(v any) []interface{} {
	l, _ := v.([]interface{})
	return l
}

// unionKeys returns the sorted union of keys present in either map.
// Terraform's own CLI plan renderer sorts map-attribute keys
// alphabetically, so this matches existing UX rather than deviating
// from it.
func unionKeys(a, b map[string]interface{}) []string {
	seen := make(map[string]struct{}, len(a)+len(b))
	keys := make([]string, 0, len(a)+len(b))
	for k := range a {
		if _, ok := seen[k]; !ok {
			seen[k] = struct{}{}
			keys = append(keys, k)
		}
	}
	for k := range b {
		if _, ok := seen[k]; !ok {
			seen[k] = struct{}{}
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	return keys
}
