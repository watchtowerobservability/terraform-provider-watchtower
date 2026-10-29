// Package resources implements one Terraform resource per as-code kind. Every resource has the same
// shape (project, name, spec as JSON); the kind table in internal/ascode says how each maps to the API.
package resources

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/watchtowerobservability/terraform-provider-watchtower/internal/ascode"
)

// Config is what the provider hands to every resource.
type Config struct {
	API, Key, Project, Org string
	HTTP                   *http.Client
}

// TypeName is the Terraform resource type of a kind: AlertRule -> watchtower_alert_rule.
func TypeName(k *ascode.Kind) string {
	var sb strings.Builder
	for i, r := range k.Name {
		if r >= 'A' && r <= 'Z' {
			if i > 0 {
				sb.WriteByte('_')
			}
			r += 'a' - 'A'
		}
		sb.WriteRune(r)
	}
	return "watchtower_" + sb.String()
}

// All returns a constructor per kind, for the provider's Resources.
func All() []func() resource.Resource {
	var out []func() resource.Resource
	for _, k := range ascode.Kinds {
		if k.Name == "Dashboard" { // takes the dashboard spec itself, with a folder
			out = append(out, func() resource.Resource { return &DashboardResource{Resource{Kind: k}} })
			continue
		}
		out = append(out, func() resource.Resource { return &Resource{Kind: k} })
	}
	return out
}

// Resource is the generic resource of one kind.
type Resource struct {
	Kind *ascode.Kind
	cfg  *Config
}

type model struct {
	ID      types.String `tfsdk:"id"`
	Project types.String `tfsdk:"project"`
	Name    types.String `tfsdk:"name"`
	Spec    types.String `tfsdk:"spec"`
	Token   types.String `tfsdk:"token"`
}

var (
	_ resource.ResourceWithConfigure   = (*Resource)(nil)
	_ resource.ResourceWithImportState = (*Resource)(nil)
)

// Metadata names the resource type.
func (r *Resource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + strings.TrimPrefix(TypeName(r.Kind), "watchtower")
}

// Schema is project + name + spec (a JSON object, e.g. jsonencode({...})). Secret fields inside spec
// take ${ENV} references only. The token a HealthToken shows once is kept in state, sensitive.
func (r *Resource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	keep := []planmodifier.String{stringplanmodifier.UseStateForUnknown()}
	resp.Schema = schema.Schema{
		Description: "Watchtower " + r.Kind.Name + ", identified by name within a project (import id: <project>/<name>).",
		Attributes: map[string]schema.Attribute{
			"id":      schema.StringAttribute{Computed: true, PlanModifiers: keep},
			"project": schema.StringAttribute{Optional: true, Computed: true, PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplaceIfConfigured(), stringplanmodifier.UseStateForUnknown()}},
			"name":    schema.StringAttribute{Required: true, PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()}},
			"spec":    schema.StringAttribute{Required: true, Description: "The manifest spec as a JSON object."},
			"token":   schema.StringAttribute{Computed: true, Sensitive: true, PlanModifiers: keep, Description: "Shown once by the API on create (HealthToken)."},
		},
	}
}

// Configure receives the provider's connection settings.
func (r *Resource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	cfg, ok := req.ProviderData.(*Config)
	if !ok {
		resp.Diagnostics.AddError("unexpected provider data", fmt.Sprintf("%T", req.ProviderData))
		return
	}
	r.cfg = cfg
}

func (r *Resource) client(project string) (*ascode.Client, error) {
	if r.cfg == nil {
		return nil, errors.New("provider is not configured")
	}
	if r.Kind.Org { // for org-scoped kinds the "project" attribute holds the org id
		if project == "" {
			project = r.cfg.Org
		}
		return ascode.NewOrg(r.cfg.API, r.cfg.Key, project, r.cfg.HTTP)
	}
	if project == "" {
		project = r.cfg.Project
	}
	return ascode.New(r.cfg.API, r.cfg.Key, project, r.cfg.HTTP)
}

func parseSpec(s string) (map[string]any, error) {
	var m map[string]any
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		return nil, fmt.Errorf("spec must be a JSON object: %w", err)
	}
	if m == nil {
		m = map[string]any{}
	}
	return m, nil
}

func canonical(m map[string]any) string {
	b, _ := json.Marshal(m) // map keys are sorted
	return string(b)
}

// Create sends the resource; a HealthToken's one-time token lands in state.
func (r *Resource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var m model
	if resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...); resp.Diagnostics.HasError() {
		return
	}
	project := m.Project.ValueString()
	c, err := r.client(project)
	if err == nil {
		var spec map[string]any
		if spec, err = parseSpec(m.Spec.ValueString()); err == nil {
			if spec, err = ascode.ExpandSecrets(r.Kind, spec); err == nil {
				var out map[string]any
				if out, err = c.Create(ctx, r.Kind, m.Name.ValueString(), spec); err == nil {
					field := r.Kind.SecretOut
					if field == "" {
						field = "token"
					}
					tok, _ := out[field].(string)
					if tok == "" { // HMAC-signed change sources return signing_key instead
						tok, _ = out["signing_key"].(string)
					}
					m.Token = types.StringValue(tok)
				}
			}
		}
	}
	if err != nil {
		resp.Diagnostics.AddError("create "+r.Kind.Name, err.Error())
		return
	}
	r.finish(&m, c.Scope())
	resp.Diagnostics.Append(resp.State.Set(ctx, m)...)
}

func (r *Resource) finish(m *model, project string) {
	m.Project = types.StringValue(project)
	m.ID = types.StringValue(project + "/" + m.Name.ValueString())
	if m.Token.IsUnknown() || m.Token.IsNull() {
		m.Token = types.StringValue("")
	}
}

// Read refreshes the spec from the live resource, keeping the stored text when nothing drifted.
func (r *Resource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var m model
	if resp.Diagnostics.Append(req.State.Get(ctx, &m)...); resp.Diagnostics.HasError() {
		return
	}
	if r.Kind.NoList { // nothing to read back: the API can only be written
		if m.Spec.ValueString() == "" { // import
			m.Spec = types.StringValue("{}")
			resp.Diagnostics.Append(resp.State.Set(ctx, m)...)
		}
		return
	}
	c, err := r.client(m.Project.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("read "+r.Kind.Name, err.Error())
		return
	}
	it, err := c.Get(ctx, r.Kind, m.Name.ValueString())
	if errors.Is(err, ascode.ErrNotFound) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("read "+r.Kind.Name, err.Error())
		return
	}
	switch want, perr := parseSpec(m.Spec.ValueString()); {
	case m.Spec.IsNull() || m.Spec.ValueString() == "": // import
		m.Spec = types.StringValue(canonical(ascode.ExportDoc(r.Kind, m.Name.ValueString(), it.Body).Spec))
	case perr == nil && !r.Kind.CreateOnly && len(ascode.Drift(r.Kind, want, it.Body)) > 0:
		m.Spec = types.StringValue(canonical(ascode.Project(want, it.Body)))
	}
	r.finish(&m, c.Scope())
	resp.Diagnostics.Append(resp.State.Set(ctx, m)...)
}

// Update replaces the resource in place.
func (r *Resource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var m, old model
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &old)...)
	if resp.Diagnostics.HasError() {
		return
	}
	c, err := r.client(m.Project.ValueString())
	if err == nil && !(r.Kind.CreateOnly && !r.Kind.ByName) { // a health token is never changed in place
		var spec map[string]any
		var it ascode.Item
		if spec, err = parseSpec(m.Spec.ValueString()); err == nil {
			spec, err = ascode.ExpandSecrets(r.Kind, spec)
		}
		if err == nil {
			it = ascode.Item{Ref: m.Name.ValueString()}
			if !r.Kind.ByName && !r.Kind.Singleton {
				it, err = c.Get(ctx, r.Kind, m.Name.ValueString())
			}
		}
		if err == nil {
			_, err = c.Update(ctx, r.Kind, it, m.Name.ValueString(), spec)
		}
	}
	if err != nil {
		resp.Diagnostics.AddError("update "+r.Kind.Name, err.Error())
		return
	}
	m.Token = old.Token
	r.finish(&m, c.Scope())
	resp.Diagnostics.Append(resp.State.Set(ctx, m)...)
}

// Delete removes the resource (a Routing tree cannot be deleted; it is only forgotten).
func (r *Resource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var m model
	if resp.Diagnostics.Append(req.State.Get(ctx, &m)...); resp.Diagnostics.HasError() || r.Kind.Singleton || r.Kind.NoDelete {
		return
	}
	c, err := r.client(m.Project.ValueString())
	if err == nil {
		it := ascode.Item{Ref: m.Name.ValueString()}
		if !r.Kind.ByName {
			it, err = c.Get(ctx, r.Kind, m.Name.ValueString())
		}
		if err == nil {
			err = c.Delete(ctx, r.Kind, it)
		}
	}
	if err != nil && !errors.Is(err, ascode.ErrNotFound) {
		resp.Diagnostics.AddError("delete "+r.Kind.Name, err.Error())
	}
}

// ImportState takes "<project>/<name>", or just "<name>" for the provider's default project.
func (r *Resource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	project, name, ok := strings.Cut(req.ID, "/")
	if !ok {
		project, name = "", req.ID
	}
	if project == "" && r.cfg != nil {
		project = r.cfg.Project
		if r.Kind.Org {
			project = r.cfg.Org
		}
	}
	if name == "" || project == "" {
		resp.Diagnostics.AddError("bad import id", "use <project>/<name>, or <name> with a provider default project")
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("project"), project)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("name"), name)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), project+"/"+name)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("token"), "")...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("spec"), "")...)
}
