// Package harness is a fixture of the kernel's extension state over SDK
// extension packages. It verifies a package against the digest its component
// names, activates the declared contributions while the component is installed
// and enabled, and settles retained data on uninstall (KOSD-C-17, KOSD-C-28,
// KOSD-C-29). It exists so the product's acceptance cases can exercise its
// contributions without a cluster; it is not an installation channel.
package harness

import (
	"fmt"

	"github.com/tsic404/kos-sdk/extension"
	"github.com/tsic404/kos-sdk/manifest"
	"github.com/tsic404/kos-sdk/packaging"
)

// Cluster is the extension state of one fixture cluster.
type Cluster struct {
	components map[string]*Component
	order      []string
	objects    map[string][]map[string]any
	residuals  map[string][]Residual
}

// Component is one installed system component and its verified package.
type Component struct {
	// Name is the component object name.
	Name string
	// Reference locates the package artifact.
	Reference string
	// Digest is the digest the install request named.
	Digest string
	// Enabled is the component's enable state; a disabled component's
	// contributions are withdrawn (KOSD-C-28).
	Enabled bool
	// Manifest is the verified declaration of the package.
	Manifest *manifest.ExtensionManifest
}

// Residual is one retained object of an uninstalled component (KOSD-C-29).
type Residual struct {
	Component string         `json:"component"`
	Kind      string         `json:"kind"`
	Object    map[string]any `json:"object"`
}

// New returns an empty cluster.
func New() *Cluster {
	return &Cluster{
		components: map[string]*Component{},
		objects:    map[string][]map[string]any{},
		residuals:  map[string][]Residual{},
	}
}

// Install verifies a package against the digest the component names and adds the
// component as enabled (KOSD-C-17). A digest mismatch rejects the install and
// leaves the cluster untouched.
func (c *Cluster) Install(name, reference string, archive []byte, digest string) error {
	verified, err := packaging.Verify(archive, packaging.Digest(digest))
	if err != nil {
		return fmt.Errorf("install component %s: %w", name, err)
	}
	if _, exists := c.components[name]; !exists {
		c.order = append(c.order, name)
	}
	c.components[name] = &Component{
		Name:      name,
		Reference: reference,
		Digest:    digest,
		Enabled:   true,
		Manifest:  &verified.Manifest,
	}
	return nil
}

// SetEnabled writes one component's enable state (KOSD-C-28).
func (c *Cluster) SetEnabled(name string, enabled bool) error {
	component, ok := c.components[name]
	if !ok {
		return fmt.Errorf("component %s is not installed", name)
	}
	component.Enabled = enabled
	return nil
}

// Disable withdraws one component's contributions and keeps its data
// (KOSD-C-28).
func (c *Cluster) Disable(name string) error { return c.SetEnabled(name, false) }

// Enable restores one component's contributions (KOSD-C-28).
func (c *Cluster) Enable(name string) error { return c.SetEnabled(name, true) }

// Manifest returns the verified declaration of one installed component.
func (c *Cluster) Manifest(name string) (*manifest.ExtensionManifest, error) {
	component, ok := c.components[name]
	if !ok {
		return nil, fmt.Errorf("component %s is not installed", name)
	}
	return component.Manifest, nil
}

// Uninstall withdraws one component and settles its data by the declared
// retention (KOSD-C-29): a retained class stays readable as a residual, a class
// declared deleted or left undeclared is dropped.
func (c *Cluster) Uninstall(name string) ([]Residual, error) {
	component, ok := c.components[name]
	if !ok {
		return nil, fmt.Errorf("component %s is not installed", name)
	}
	retention := component.Manifest.UninstallRetention.Contributions
	for _, class := range component.Manifest.Contributions.ObjectClasses {
		stored := c.objects[class.Kind]
		action, declared := retention[class.Kind]
		if !declared || action == manifest.Delete {
			delete(c.objects, class.Kind)
			continue
		}
		for _, object := range stored {
			c.residuals[name] = append(c.residuals[name], Residual{Component: name, Kind: class.Kind, Object: object})
		}
		delete(c.objects, class.Kind)
	}
	delete(c.components, name)
	for index, ordered := range c.order {
		if ordered == name {
			c.order = append(c.order[:index], c.order[index+1:]...)
			break
		}
	}
	return c.Residuals(name), nil
}

// Seed stores one object as if the product's controller wrote it.
func (c *Cluster) Seed(kind string, object map[string]any) {
	c.objects[kind] = append(c.objects[kind], object)
}

// Objects returns the stored objects of one class.
func (c *Cluster) Objects(kind string) []map[string]any {
	return append([]map[string]any(nil), c.objects[kind]...)
}

// Residuals returns the retained objects of one uninstalled component.
func (c *Cluster) Residuals(name string) []Residual {
	return append([]Residual(nil), c.residuals[name]...)
}

// ObjectClasses returns the classes of every active component (KOSD-C-19,
// KOSD-C-28).
func (c *Cluster) ObjectClasses() []extension.ObjectClassDeclaration {
	classes := []extension.ObjectClassDeclaration{}
	for _, component := range c.active() {
		classes = append(classes, component.Manifest.Contributions.ObjectClasses...)
	}
	return classes
}

// Serves reports whether one object class is served: a class is served only
// while a component declaring it is active (KOSD-C-19, KOSD-C-28).
func (c *Cluster) Serves(kind string) bool {
	for _, class := range c.ObjectClasses() {
		if class.Kind == kind {
			return true
		}
	}
	return false
}

// Pages returns the pages of every active component (KOSD-C-28).
func (c *Cluster) Pages() []map[string]any {
	pages := []map[string]any{}
	for _, component := range c.active() {
		pages = append(pages, component.Manifest.Contributions.Pages...)
	}
	return pages
}

// KernelSurface is what an install must not change: the kernel kind set, the
// contributed capability names and the controller write scopes (KOSD-AC-72,
// KOSD-C-29).
type KernelSurface struct {
	CoreKinds       []string
	Capabilities    []string
	ControllerScope []string
}

// Surface reports the kernel surface the active components contribute to.
func (c *Cluster) Surface() KernelSurface {
	surface := KernelSurface{CoreKinds: append([]string(nil), extension.CoreObjectKinds...)}
	for _, component := range c.active() {
		for _, capability := range component.Manifest.Contributions.Capabilities {
			name, _ := capability["name"].(string)
			surface.Capabilities = append(surface.Capabilities, name)
		}
		for _, controller := range component.Manifest.Contributions.Controllers {
			for _, scope := range controller.WriteScopes {
				surface.ControllerScope = append(surface.ControllerScope,
					controller.Name+":"+scope.APIGroup+"/"+scope.Kind)
			}
		}
	}
	return surface
}

// active returns every installed component whose contributions are active.
func (c *Cluster) active() []*Component {
	components := make([]*Component, 0, len(c.order))
	for _, name := range c.order {
		component := c.components[name]
		if component != nil && component.Enabled {
			components = append(components, component)
		}
	}
	return components
}
