package resources

import (
	"context"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-go/tftypes"

	"github.com/watchtowerobservability/terraform-provider-watchtower/internal/ascode"
	"github.com/watchtowerobservability/terraform-provider-watchtower/internal/ascode/ascodetest"
)

type dashHarness struct {
	t   *testing.T
	r   *DashboardResource
	srv *ascodetest.Server
	sch resource.SchemaResponse
}

func newDashHarness(t *testing.T) *dashHarness {
	t.Helper()
	srv := ascodetest.New(t)
	h := &dashHarness{t: t, srv: srv, r: &DashboardResource{Resource{Kind: ascode.ByKind("Dashboard")}}}
	h.r.Configure(context.Background(), resource.ConfigureRequest{ProviderData: &Config{API: srv.URL, Key: "test-key", Project: ascodetest.Project}}, &resource.ConfigureResponse{})
	h.r.Schema(context.Background(), resource.SchemaRequest{}, &h.sch)
	return h
}

// plan builds the planned value; a nil spec attribute is null (not set).
func (h *dashHarness) plan(folder, specJSON, specYAML any) tfsdk.Plan {
	typ := h.sch.Schema.Type().TerraformType(context.Background())
	s := func(v any) tftypes.Value { return tftypes.NewValue(tftypes.String, v) }
	return tfsdk.Plan{Schema: h.sch.Schema, Raw: tftypes.NewValue(typ, map[string]tftypes.Value{
		"id": s(tftypes.UnknownValue), "project": s(nil), "name": s("Checkout"),
		"folder": s(folder), "spec_json": s(specJSON), "spec_yaml": s(specYAML),
	})}
}

func (h *dashHarness) emptyState() tfsdk.State {
	return tfsdk.State{Schema: h.sch.Schema, Raw: tftypes.NewValue(h.sch.Schema.Type().TerraformType(context.Background()), nil)}
}

func (h *dashHarness) create(p tfsdk.Plan) tfsdk.State {
	h.t.Helper()
	resp := resource.CreateResponse{State: h.emptyState()}
	h.r.Create(context.Background(), resource.CreateRequest{Plan: p}, &resp)
	if resp.Diagnostics.HasError() {
		h.t.Fatal(resp.Diagnostics)
	}
	return resp.State
}

func (h *dashHarness) read(st tfsdk.State) tfsdk.State {
	h.t.Helper()
	resp := resource.ReadResponse{State: st}
	h.r.Read(context.Background(), resource.ReadRequest{State: st}, &resp)
	if resp.Diagnostics.HasError() {
		h.t.Fatal(resp.Diagnostics)
	}
	return resp.State
}

func (h *dashHarness) attr(st tfsdk.State, name string) string {
	h.t.Helper()
	var v *string
	if d := st.GetAttribute(context.Background(), path.Root(name), &v); d.HasError() {
		h.t.Fatal(d)
	}
	if v == nil {
		return "<null>"
	}
	return *v
}

const dashJSON = `{"version":1,"variables":[],"panels":[{"id":"rate","title":"Rate","type":"timeseries","grid":{"x":0,"y":0,"w":12,"h":6},"query":{"kind":"promql","text":"up"}}]}`

func TestTerraformDashboardPlanStable(t *testing.T) {
	h := newDashHarness(t)
	st := h.create(h.plan(nil, dashJSON, nil))
	if h.srv.Count("Dashboard") != 1 {
		t.Fatal("dashboard not created")
	}
	// Refresh keeps the configured text byte for byte: no perpetual diff.
	for i := 0; i < 2; i++ {
		st = h.read(st)
		if got := h.attr(st, "spec_json"); got != dashJSON {
			t.Fatalf("read changed the spec text:\n%s", got)
		}
		if h.attr(st, "spec_yaml") != "<null>" || h.attr(st, "id") != ascodetest.Project+"/Checkout" {
			t.Fatalf("state: yaml=%s id=%s", h.attr(st, "spec_yaml"), h.attr(st, "id"))
		}
	}
	// The same dashboard written as YAML plans the same API body.
	yamlSpec := "version: 1\nvariables: []\npanels:\n  - {id: rate, title: Rate, type: timeseries, grid: {x: 0, y: 0, w: 12, h: 6}, query: {kind: promql, text: up}}\n"
	h2 := newDashHarness(t)
	st2 := h2.create(h2.plan(nil, nil, yamlSpec))
	if got := h2.attr(h2.read(st2), "spec_yaml"); got != yamlSpec {
		t.Fatalf("yaml text changed on read: %q", got)
	}
	a, b := h.srv.Body("Dashboard", "Checkout")["spec"], h2.srv.Body("Dashboard", "Checkout")["spec"]
	if canonical(a.(map[string]any)) != canonical(b.(map[string]any)) {
		t.Fatalf("json and yaml created different dashboards:\n%v\n%v", a, b)
	}
}

func TestTerraformDashboardDriftIsReported(t *testing.T) {
	h := newDashHarness(t)
	st := h.create(h.plan(nil, dashJSON, nil))
	live := h.srv.Body("Dashboard", "Checkout")["spec"].(map[string]any)
	live["panels"].([]any)[0].(map[string]any)["title"] = "Changed in the UI"
	if got := h.attr(h.read(st), "spec_json"); !strings.Contains(got, "Changed in the UI") {
		t.Fatalf("drift not surfaced: %s", got)
	}
}

func TestTerraformDashboardNeedsExactlyOneSpec(t *testing.T) {
	h := newDashHarness(t)
	for _, c := range []struct {
		j, y any
		bad  bool
	}{{dashJSON, nil, false}, {nil, "version: 1", false}, {dashJSON, "version: 1", true}, {nil, nil, true}} {
		typ := h.sch.Schema.Type().TerraformType(context.Background())
		s := func(v any) tftypes.Value { return tftypes.NewValue(tftypes.String, v) }
		cfg := tfsdk.Config{Schema: h.sch.Schema, Raw: tftypes.NewValue(typ, map[string]tftypes.Value{
			"id": s(nil), "project": s(nil), "name": s("x"), "folder": s(nil), "spec_json": s(c.j), "spec_yaml": s(c.y),
		})}
		var resp resource.ValidateConfigResponse
		h.r.ValidateConfig(context.Background(), resource.ValidateConfigRequest{Config: cfg}, &resp)
		if resp.Diagnostics.HasError() != c.bad {
			t.Errorf("json=%v yaml=%v: errors=%v", c.j, c.y, resp.Diagnostics)
		}
	}
}
