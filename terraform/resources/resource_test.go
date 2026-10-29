package resources

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-go/tftypes"

	"github.com/watchtowerobservability/terraform-provider-watchtower/internal/ascode"
	"github.com/watchtowerobservability/terraform-provider-watchtower/internal/ascode/ascodetest"
)

type harness struct {
	t   *testing.T
	r   *Resource
	srv *ascodetest.Server
	sch resource.SchemaResponse
}

func newHarness(t *testing.T, kind string) *harness {
	t.Helper()
	srv := ascodetest.New(t)
	h := &harness{t: t, srv: srv, r: &Resource{Kind: ascode.ByKind(kind)}}
	h.r.Configure(context.Background(), resource.ConfigureRequest{ProviderData: &Config{API: srv.URL, Key: "test-key", Project: ascodetest.Project}}, &resource.ConfigureResponse{})
	h.r.Schema(context.Background(), resource.SchemaRequest{}, &h.sch)
	return h
}

func (h *harness) raw(project, name, spec any, unknown bool) tftypes.Value {
	typ := h.sch.Schema.Type().TerraformType(context.Background())
	un := func(v any) tftypes.Value {
		if unknown {
			return tftypes.NewValue(tftypes.String, tftypes.UnknownValue)
		}
		return tftypes.NewValue(tftypes.String, v)
	}
	return tftypes.NewValue(typ, map[string]tftypes.Value{
		"id": un(nil), "token": un(nil),
		"project": tftypes.NewValue(tftypes.String, project),
		"name":    tftypes.NewValue(tftypes.String, name),
		"spec":    tftypes.NewValue(tftypes.String, spec),
	})
}

func (h *harness) emptyState() tfsdk.State {
	return tfsdk.State{Schema: h.sch.Schema, Raw: tftypes.NewValue(h.sch.Schema.Type().TerraformType(context.Background()), nil)}
}

func (h *harness) create(spec string) tfsdk.State {
	h.t.Helper()
	resp := resource.CreateResponse{State: h.emptyState()}
	h.r.Create(context.Background(), resource.CreateRequest{Plan: tfsdk.Plan{Schema: h.sch.Schema, Raw: h.raw(nil, "api-health", spec, true)}}, &resp)
	// project and name are known in a real plan; mimic that
	if resp.Diagnostics.HasError() {
		h.t.Fatal(resp.Diagnostics)
	}
	return resp.State
}

func (h *harness) read(st tfsdk.State) resource.ReadResponse {
	resp := resource.ReadResponse{State: st}
	h.r.Read(context.Background(), resource.ReadRequest{State: st}, &resp)
	if resp.Diagnostics.HasError() {
		h.t.Fatal(resp.Diagnostics)
	}
	return resp
}

func attr(h *harness, st tfsdk.State, name string) string {
	var v string
	h.t.Helper()
	if d := st.GetAttribute(context.Background(), path.Root(name), &v); d.HasError() {
		h.t.Fatal(d)
	}
	return v
}

const spec1 = `{"config":{"url":"https://example.com/health"},"env_id":"env_1","interval_s":60,"kind":"http"}`

func TestTerraformMonitorCRUD(t *testing.T) {
	h := newHarness(t, "Monitor")
	ctx := context.Background()
	st := h.create(spec1)
	if h.srv.Count("Monitor") != 1 || attr(h, st, "id") != ascodetest.Project+"/api-health" {
		t.Fatalf("create: monitors=%d id=%q", h.srv.Count("Monitor"), attr(h, st, "id"))
	}

	// No drift: the stored spec text is kept byte for byte.
	if got := attr(h, h.read(st).State, "spec"); got != spec1 {
		t.Fatalf("read changed an undrifted spec: %s", got)
	}

	// Someone edits the monitor out of band: Read reports the live value.
	c, _ := ascode.New(h.srv.URL, "test-key", ascodetest.Project, nil)
	it, err := c.Get(ctx, h.r.Kind, "api-health")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Update(ctx, h.r.Kind, it, "api-health", map[string]any{"env_id": "env_1", "kind": "http", "interval_s": 5, "config": map[string]any{"url": "https://example.com/health"}}); err != nil {
		t.Fatal(err)
	}
	drifted := attr(h, h.read(st).State, "spec")
	if !strings.Contains(drifted, `"interval_s":5`) {
		t.Fatalf("drift not reported: %s", drifted)
	}

	// Update puts the configured spec back.
	upd := resource.UpdateResponse{State: st}
	h.r.Update(ctx, resource.UpdateRequest{
		Plan:  tfsdk.Plan{Schema: h.sch.Schema, Raw: h.raw(ascodetest.Project, "api-health", spec1, false)},
		State: st,
	}, &upd)
	if upd.Diagnostics.HasError() {
		t.Fatal(upd.Diagnostics)
	}
	if got := h.srv.Body("Monitor", "api-health")["interval_s"]; got != float64(60) {
		t.Fatalf("update did not reach the API: %v", got)
	}

	// Delete, then Read forgets it.
	del := resource.DeleteResponse{State: st}
	h.r.Delete(ctx, resource.DeleteRequest{State: st}, &del)
	if del.Diagnostics.HasError() || h.srv.Count("Monitor") != 0 {
		t.Fatalf("delete: %v monitors=%d", del.Diagnostics, h.srv.Count("Monitor"))
	}
	if !h.read(st).State.Raw.IsNull() {
		t.Fatal("Read must remove a resource that is gone")
	}
}

func TestTerraformImportState(t *testing.T) {
	h := newHarness(t, "Monitor")
	ctx := context.Background()
	c, _ := ascode.New(h.srv.URL, "test-key", ascodetest.Project, nil)
	if _, err := c.Create(ctx, h.r.Kind, "api-health", map[string]any{"env_id": "env_1", "kind": "http", "interval_s": 60, "config": map[string]any{}}); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{ascodetest.Project + "/api-health", "api-health"} {
		imp := resource.ImportStateResponse{State: h.emptyState()}
		h.r.ImportState(ctx, resource.ImportStateRequest{ID: id}, &imp)
		if imp.Diagnostics.HasError() {
			t.Fatal(imp.Diagnostics)
		}
		st := h.read(imp.State).State
		var spec map[string]any
		if err := json.Unmarshal([]byte(attr(h, st, "spec")), &spec); err != nil {
			t.Fatal(err)
		}
		if attr(h, st, "name") != "api-health" || spec["interval_s"] != float64(60) || spec["id"] != nil || spec["created_at"] != nil {
			t.Fatalf("import id %q: name=%q spec=%v", id, attr(h, st, "name"), spec)
		}
	}
}

func TestTerraformHealthTokenShownOnce(t *testing.T) {
	h := newHarness(t, "HealthToken")
	st := h.create(`{}`)
	if !strings.HasPrefix(attr(h, st, "token"), "wth_once_") {
		t.Fatalf("token not in state: %q", attr(h, st, "token"))
	}
	if attr(h, h.read(st).State, "token") != attr(h, st, "token") {
		t.Fatal("read must keep the token it cannot see again")
	}
}

func TestEveryKindHasAResource(t *testing.T) {
	if len(All()) != len(ascode.Kinds) {
		t.Fatalf("want %d resources, got %d", len(ascode.Kinds), len(All()))
	}
	if got := TypeName(ascode.ByKind("MaintenanceWindow")); got != "watchtower_maintenance_window" {
		t.Fatal(got)
	}
}
