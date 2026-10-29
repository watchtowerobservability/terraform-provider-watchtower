package resources

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"gopkg.in/yaml.v3"

	"github.com/watchtowerobservability/terraform-provider-watchtower/internal/ascode"
)

// DashboardResource is watchtower_dashboard {name, folder, spec_json | spec_yaml}: unlike the
// generic resource it takes the dashboard spec itself (not a manifest spec) as JSON or YAML, and
// can place the dashboard in a folder. It reuses the generic resource's client and import path.
type DashboardResource struct{ Resource }

type dashModel struct {
	ID       types.String `tfsdk:"id"`
	Project  types.String `tfsdk:"project"`
	Name     types.String `tfsdk:"name"`
	Folder   types.String `tfsdk:"folder"`
	SpecJSON types.String `tfsdk:"spec_json"`
	SpecYAML types.String `tfsdk:"spec_yaml"`
}

var (
	_ resource.ResourceWithConfigure      = (*DashboardResource)(nil)
	_ resource.ResourceWithImportState    = (*DashboardResource)(nil)
	_ resource.ResourceWithValidateConfig = (*DashboardResource)(nil)
)

// Schema is project + name + folder + exactly one of spec_json and spec_yaml.
func (r *DashboardResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	keep := []planmodifier.String{stringplanmodifier.UseStateForUnknown()}
	resp.Schema = schema.Schema{
		Description: "A Watchtower dashboard, identified by its title within a project (import id: <project>/<title>).",
		Attributes: map[string]schema.Attribute{
			"id":        schema.StringAttribute{Computed: true, PlanModifiers: keep},
			"project":   schema.StringAttribute{Optional: true, Computed: true, PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplaceIfConfigured(), stringplanmodifier.UseStateForUnknown()}},
			"name":      schema.StringAttribute{Required: true, PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()}, Description: "The dashboard title."},
			"folder":    schema.StringAttribute{Optional: true, Description: "Id of a dashboard folder."},
			"spec_json": schema.StringAttribute{Optional: true, Description: "The dashboard spec as JSON, e.g. jsonencode({version = 1, panels = [...]}). Exactly one of spec_json and spec_yaml."},
			"spec_yaml": schema.StringAttribute{Optional: true, Description: "The dashboard spec as YAML, e.g. file(\"checkout.yaml\")."},
		},
	}
}

// ValidateConfig requires exactly one spec attribute.
func (r *DashboardResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var j, y types.String
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("spec_json"), &j)...)
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("spec_yaml"), &y)...)
	if resp.Diagnostics.HasError() || j.IsUnknown() || y.IsUnknown() {
		return
	}
	if j.IsNull() == y.IsNull() {
		resp.Diagnostics.AddError("invalid dashboard", "set exactly one of spec_json and spec_yaml")
	}
}

// spec decodes whichever attribute is set into the JSON object the API takes.
func (m dashModel) spec() (map[string]any, error) {
	if !m.SpecJSON.IsNull() && m.SpecJSON.ValueString() != "" {
		return parseSpec(m.SpecJSON.ValueString())
	}
	var raw map[string]any
	if err := yaml.Unmarshal([]byte(m.SpecYAML.ValueString()), &raw); err != nil {
		return nil, fmt.Errorf("spec_yaml: %w", err)
	}
	b, err := json.Marshal(raw) // numbers and maps look like the API's
	if err != nil {
		return nil, fmt.Errorf("spec_yaml: %w", err)
	}
	return parseSpec(string(b))
}

// setSpec writes spec back into the attribute the user chose.
func (m *dashModel) setSpec(spec map[string]any) error {
	if !m.SpecYAML.IsNull() && m.SpecYAML.ValueString() != "" {
		b, err := yaml.Marshal(spec)
		if err != nil {
			return err
		}
		m.SpecYAML = types.StringValue(string(b))
		return nil
	}
	m.SpecJSON = types.StringValue(canonical(spec))
	return nil
}

func (r *DashboardResource) setFolder(ctx context.Context, m dashModel, old *dashModel) error {
	if m.Folder.IsNull() && (old == nil || old.Folder.IsNull()) {
		return nil
	}
	c, err := r.client(m.Project.ValueString())
	if err != nil {
		return err
	}
	it, err := c.Get(ctx, r.Kind, m.Name.ValueString())
	if err != nil {
		return err
	}
	var folder any
	if !m.Folder.IsNull() {
		folder = m.Folder.ValueString()
	}
	return c.Do(ctx, "PUT", r.Kind.Path+"/"+it.Ref+"/folder", nil, map[string]any{"folder_id": folder}, nil)
}

func (r *DashboardResource) finishDash(m *dashModel, project string) {
	m.Project = types.StringValue(project)
	m.ID = types.StringValue(project + "/" + m.Name.ValueString())
}

// Create sends the dashboard and, when set, moves it into the folder.
func (r *DashboardResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var m dashModel
	if resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...); resp.Diagnostics.HasError() {
		return
	}
	c, err := r.client(m.Project.ValueString())
	if err == nil {
		var spec map[string]any
		if spec, err = m.spec(); err == nil {
			_, err = c.Create(ctx, r.Kind, m.Name.ValueString(), map[string]any{"spec": spec})
		}
	}
	if err == nil {
		m.Project = types.StringValue(c.Project)
		err = r.setFolder(ctx, m, nil)
	}
	if err != nil {
		resp.Diagnostics.AddError("create Dashboard", err.Error())
		return
	}
	r.finishDash(&m, c.Project)
	resp.Diagnostics.Append(resp.State.Set(ctx, m)...)
}

// Read keeps the configured text unless the live spec drifted from it; an import (no text yet)
// takes the live spec as JSON.
func (r *DashboardResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var m dashModel
	if resp.Diagnostics.Append(req.State.Get(ctx, &m)...); resp.Diagnostics.HasError() {
		return
	}
	c, err := r.client(m.Project.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("read Dashboard", err.Error())
		return
	}
	it, err := c.Get(ctx, r.Kind, m.Name.ValueString())
	if errors.Is(err, ascode.ErrNotFound) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("read Dashboard", err.Error())
		return
	}
	live, _ := it.Body["spec"].(map[string]any)
	switch want, werr := m.spec(); {
	case m.SpecJSON.ValueString() == "" && m.SpecYAML.ValueString() == "": // import
		m.SpecJSON = types.StringValue(canonical(live))
	case werr == nil && len(ascode.Drift(r.Kind, map[string]any{"spec": want}, it.Body)) > 0:
		kept, _ := ascode.Project(map[string]any{"spec": want}, it.Body)["spec"].(map[string]any)
		if err = m.setSpec(kept); err != nil {
			resp.Diagnostics.AddError("read Dashboard", err.Error())
			return
		}
	}
	r.finishDash(&m, c.Project)
	resp.Diagnostics.Append(resp.State.Set(ctx, m)...)
}

// Update replaces the spec (under If-Match, through the shared client) and moves the folder.
func (r *DashboardResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var m, old dashModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &old)...)
	if resp.Diagnostics.HasError() {
		return
	}
	c, err := r.client(m.Project.ValueString())
	if err == nil {
		var spec map[string]any
		var it ascode.Item
		if spec, err = m.spec(); err == nil {
			it, err = c.Get(ctx, r.Kind, m.Name.ValueString())
		}
		if err == nil {
			_, err = c.Update(ctx, r.Kind, it, m.Name.ValueString(), map[string]any{"spec": spec})
		}
	}
	if err == nil {
		m.Project = types.StringValue(c.Project)
		err = r.setFolder(ctx, m, &old)
	}
	if err != nil {
		resp.Diagnostics.AddError("update Dashboard", err.Error())
		return
	}
	r.finishDash(&m, c.Project)
	resp.Diagnostics.Append(resp.State.Set(ctx, m)...)
}

// Delete removes the dashboard (its versions are kept by the API).
func (r *DashboardResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var m dashModel
	if resp.Diagnostics.Append(req.State.Get(ctx, &m)...); resp.Diagnostics.HasError() {
		return
	}
	c, err := r.client(m.Project.ValueString())
	if err == nil {
		var it ascode.Item
		if it, err = c.Get(ctx, r.Kind, m.Name.ValueString()); err == nil {
			err = c.Delete(ctx, r.Kind, it)
		}
	}
	if err != nil && !errors.Is(err, ascode.ErrNotFound) {
		resp.Diagnostics.AddError("delete Dashboard", err.Error())
	}
}

// ImportState takes "<project>/<title>", or just "<title>" for the provider's default project.
func (r *DashboardResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	project, name, ok := strings.Cut(req.ID, "/")
	if !ok {
		project, name = "", req.ID
	}
	if project == "" && r.cfg != nil {
		project = r.cfg.Project
	}
	if name == "" || project == "" {
		resp.Diagnostics.AddError("bad import id", "use <project>/<title>, or <title> with a provider default project")
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("project"), project)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("name"), name)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), project+"/"+name)...)
}
