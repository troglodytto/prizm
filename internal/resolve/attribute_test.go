package resolve

import "testing"

func layersFixture() []Layer {
	return []Layer{
		{Name: "group", Kind: LayerGroup, Vars: map[string]string{
			"_PRIZM_CLUSTER": "cluster.internal",
			"SHADOWED":       "from-group",
		}},
		{Name: "repo-shared", Kind: LayerRepoShared, Vars: map[string]string{
			"LOG_LEVEL": "debug",
			"SHADOWED":  "from-repo",
		}},
		{Name: "shared:db", Kind: LayerSharedGroup, SharedGroupID: 7, Vars: map[string]string{
			"_PRIZM_DB_URL": "postgres://h/db",
			"SHADOWED":      "from-bag",
		}},
		{Name: "local+backend", Kind: LayerWorkflowRepo, Vars: map[string]string{
			"DB_URL":   "${_PRIZM_DB_URL}",
			"SHADOWED": "from-workflow",
		}},
	}
}

func TestAttributeFindsTheDefiningLayer(t *testing.T) {
	layers := layersFixture()

	for _, tt := range []struct {
		key       string
		wantLayer string
		wantKind  LayerKind
	}{
		{"_PRIZM_CLUSTER", "group", LayerGroup},
		{"LOG_LEVEL", "repo-shared", LayerRepoShared},
		{"_PRIZM_DB_URL", "shared:db", LayerSharedGroup},
		{"DB_URL", "local+backend", LayerWorkflowRepo},
	} {
		origin, ok := Attribute(layers, tt.key)
		if !ok {
			t.Fatalf("Attribute(%q) not found", tt.key)
		}
		if origin.Layer != tt.wantLayer || origin.Kind != tt.wantKind {
			t.Errorf("Attribute(%q) = %q/%v, want %q/%v", tt.key, origin.Layer, origin.Kind, tt.wantLayer, tt.wantKind)
		}
	}
}

func TestAttributeReturnsTheWinningLayerNotTheFirst(t *testing.T) {
	origin, ok := Attribute(layersFixture(), "SHADOWED")
	if !ok {
		t.Fatal("Attribute(SHADOWED) not found")
	}
	if origin.Layer != "local+backend" || origin.Template != "from-workflow" {
		t.Errorf("Attribute() = %q/%q, want the highest-precedence definition", origin.Layer, origin.Template)
	}
}

func TestAttributeCarriesTheSharedGroupID(t *testing.T) {
	origin, _ := Attribute(layersFixture(), "_PRIZM_DB_URL")
	if origin.SharedGroupID != 7 {
		t.Errorf("SharedGroupID = %d, want 7 — sync needs it to write back", origin.SharedGroupID)
	}
}

func TestAttributeUnknownKey(t *testing.T) {
	if _, ok := Attribute(layersFixture(), "NOPE"); ok {
		t.Error("Attribute(unknown) ok = true, want false")
	}
}

func TestSoleRef(t *testing.T) {
	for _, tt := range []struct {
		template string
		want     string
		wantOK   bool
	}{
		{"${_PRIZM_DB_URL}", "_PRIZM_DB_URL", true},
		{"  ${_PRIZM_DB_URL}  ", "_PRIZM_DB_URL", true},
		{"postgres://${_PRIZM_HOST}/db", "", false},
		{"${A}${B}", "", false},
		{"plain", "", false},
		{"", "", false},
	} {
		got, ok := SoleRef(tt.template)
		if ok != tt.wantOK || (ok && got != tt.want) {
			t.Errorf("SoleRef(%q) = (%q, %v), want (%q, %v)", tt.template, got, ok, tt.want, tt.wantOK)
		}
	}
}

func TestIsTemplate(t *testing.T) {
	for value, want := range map[string]bool{
		"${A}":        true,
		"a${A}b":      true,
		"plain":       false,
		"costs $100":  false,
		"regex ^foo$": false,
	} {
		if got := IsTemplate(value); got != want {
			t.Errorf("IsTemplate(%q) = %v, want %v", value, got, want)
		}
	}
}
