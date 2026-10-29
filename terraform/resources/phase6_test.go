package resources

import (
	"context"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"

	"github.com/watchtowerobservability/terraform-provider-watchtower/internal/ascode"
	"github.com/watchtowerobservability/terraform-provider-watchtower/internal/ascode/ascodetest"
)

func orgHarness(t *testing.T, kind string) *harness {
	t.Helper()
	h := newHarness(t, kind)
	h.r.cfg.Org = ascodetest.Org
	return h
}

func (h *harness) make(name, spec string) (tfsdk.State, resource.CreateResponse) {
	h.t.Helper()
	resp := resource.CreateResponse{State: h.emptyState()}
	h.r.Create(context.Background(), resource.CreateRequest{Plan: tfsdk.Plan{Schema: h.sch.Schema, Raw: h.raw(nil, name, spec, true)}}, &resp)
	return resp.State, resp
}

func (h *harness) must(name, spec string) tfsdk.State {
	h.t.Helper()
	st, resp := h.make(name, spec)
	if resp.Diagnostics.HasError() {
		h.t.Fatal(resp.Diagnostics)
	}
	return st
}

func (h *harness) remove(st tfsdk.State) {
	h.t.Helper()
	del := resource.DeleteResponse{State: st}
	h.r.Delete(context.Background(), resource.DeleteRequest{State: st}, &del)
	if del.Diagnostics.HasError() {
		h.t.Fatal(del.Diagnostics)
	}
}

func TestRetentionRuleResourceCRUD(t *testing.T) {
	h := newHarness(t, "RetentionRule")
	const spec = `{"days":30,"match":{},"signal":"logs"}`
	st := h.must("10", spec) // the name is the rule's priority
	if h.srv.Count("RetentionRule") != 1 || h.srv.Body("RetentionRule", "10") != nil && h.srv.Body("RetentionRule", "10")["priority"] != float64(10) {
		t.Fatalf("priority must be sent as a number: %v", h.srv.Body("RetentionRule", "10"))
	}
	if got := attr(h, h.read(st).State, "spec"); got != spec {
		t.Fatalf("undrifted spec changed: %s", got)
	}
	upd := resource.UpdateResponse{State: st}
	h.r.Update(context.Background(), resource.UpdateRequest{
		Plan:  tfsdk.Plan{Schema: h.sch.Schema, Raw: h.raw(ascodetest.Project, "10", `{"days":90,"match":{},"signal":"logs"}`, false)},
		State: st,
	}, &upd)
	if upd.Diagnostics.HasError() {
		t.Fatal(upd.Diagnostics)
	}
	h.remove(st)
	if h.srv.Count("RetentionRule") != 0 {
		t.Fatal("delete did not reach the API")
	}
}

func TestSecretFieldNotReadBack(t *testing.T) {
	// change source: the one-time secret lands in state (sensitive) and is never fetched again
	cs := newHarness(t, "ChangeSource")
	st := cs.must("unleash", `{"mapping":{}}`)
	if !strings.HasPrefix(attr(cs, st, "token"), "cs_once_") {
		t.Fatalf("secret not kept in state: %q", attr(cs, st, "token"))
	}
	if attr(cs, cs.read(st).State, "token") != attr(cs, st, "token") {
		t.Fatal("read must keep the secret it cannot see again")
	}
	if _, leaked := cs.srv.Body("ChangeSource", "unleash")["secret"]; leaked {
		t.Fatal("the fake stored the secret; the API must not")
	}

	// secret store: the credential is an ${ENV} reference in state and never read back
	ss := orgHarness(t, "SecretStore")
	t.Setenv("WT_TEST_VAULT_TOKEN", "s3cr3t-value")
	spec := `{"config":{"address":"https://vault.example.com"},"credential":{"token":"${WT_TEST_VAULT_TOKEN}"},"kind":"vault"}`
	st = ss.must("vault", spec)
	got := attr(ss, ss.read(st).State, "spec")
	if strings.Contains(got, "s3cr3t-value") || got != spec {
		t.Fatalf("spec must keep the reference, not the value: %s", got)
	}
	if len(ss.srv.Secrets) != 1 || !strings.Contains(ss.srv.Secrets[0], "s3cr3t-value") {
		t.Fatalf("API did not receive the expanded credential: %v", ss.srv.Secrets)
	}
	_, resp := ss.make("literal", strings.Replace(spec, "${WT_TEST_VAULT_TOKEN}", "plain-text", 1))
	if !resp.Diagnostics.HasError() {
		t.Fatal("a literal credential must be rejected")
	}
}

func TestOrgScopedResources(t *testing.T) {
	al := orgHarness(t, "IpAllowlist")
	st := al.must("ingest", `{"cidrs":["10.0.0.0/8"],"enabled":true}`)
	if attr(al, st, "id") != ascodetest.Org+"/ingest" {
		t.Fatalf("id = %q", attr(al, st, "id"))
	}
	al.remove(st) // reset by PUT: the API has no DELETE
	for _, c := range al.srv.Calls {
		if strings.HasPrefix(c, "DELETE") {
			t.Fatalf("allowlist delete must be a reset PUT: %v", al.srv.Calls)
		}
	}

	sso := orgHarness(t, "OrgSso")
	st = sso.must("sso", `{"enforce_sso":true,"require_mfa":true}`)
	if got := attr(sso, sso.read(st).State, "spec"); !strings.Contains(got, `"enforce_sso":true`) {
		t.Fatalf("sso not read back: %s", got)
	}
	sso.remove(st) // a singleton is only forgotten
}

func TestFleetConfigDeleteOnlyForgets(t *testing.T) {
	h := newHarness(t, "FleetConfig")
	st := h.must("edge", `{"kind":"collector"}`)
	h.remove(st)
	for _, c := range h.srv.Calls {
		if strings.HasPrefix(c, "DELETE") {
			t.Fatalf("no DELETE route exists for fleet configs: %v", h.srv.Calls)
		}
	}
}

func TestImportStatePhase6Kinds(t *testing.T) {
	for kind, id := range map[string]string{
		"RetentionRule":     ascodetest.Project + "/10",
		"IpAllowlist":       ascodetest.Org + "/org",
		"AttributeRegistry": ascodetest.Project + "/user.id",
	} {
		h := orgHarness(t, kind)
		imp := resource.ImportStateResponse{State: h.emptyState()}
		h.r.ImportState(context.Background(), resource.ImportStateRequest{ID: id}, &imp)
		if imp.Diagnostics.HasError() {
			t.Fatalf("%s: %v", kind, imp.Diagnostics)
		}
		if got := attr(h, imp.State, "id"); got != id {
			t.Fatalf("%s: id %q", kind, got)
		}
	}
}

func TestNewOrgClientNeedsOrg(t *testing.T) {
	if _, err := ascode.NewOrg("http://x", "k", "", nil); err == nil {
		t.Fatal("org id must be required")
	}
	c, err := ascode.NewOrg("http://x", "k", "org_1", nil)
	if err != nil || c.Scope() != "org_1" {
		t.Fatal(err, c)
	}
}
