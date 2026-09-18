package project

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// rule recognises one kind of project by the packages its composer.json
// requires, or by a file only that kind of project has.
type rule struct {
	template string
	packages []string
	files    []string
}

// rules are checked in order, most specific first (project-folders.feature,
// "The config template is detected from the folder").
//
// WARNING: The order is the detection. Statamic is a Laravel app, and Laravel
// and Drupal both require Symfony components, so a rule moved below a more
// general one is never reached.
var rules = []rule{
	{template: "statamic", packages: []string{"statamic/cms"}},
	{template: "craft", packages: []string{"craftcms/cms"}, files: []string{"craft"}},
	{
		template: "drupal",
		packages: []string{"drupal/core", "drupal/core-recommended"},
		files:    []string{"core/lib/Drupal.php", "web/core/lib/Drupal.php"},
	},
	{template: "laravel", packages: []string{"laravel/framework"}, files: []string{"artisan"}},
	{template: "symfony", packages: []string{"symfony/framework-bundle"}, files: []string{"bin/console"}},
	{
		template: "wordpress",
		packages: []string{"roots/wordpress", "johnpbloch/wordpress"},
		files:    []string{"wp-config.php", "wp-config-sample.php", "wp-includes/version.php"},
	},
	{template: "kirby", packages: []string{"getkirby/cms"}, files: []string{"kirby/bootstrap.php"}},
}

// DetectedTemplates lists every config template Detect can propose, so a test
// can prove each is one Wharf has.
func DetectedTemplates() []string {
	out := make([]string, len(rules))
	for i, r := range rules {
		out[i] = r.template
	}
	return out
}

// Detect proposes the config template for the project in dir, or "" when
// nothing in it names one — plain PHP needs none. It only reads: a folder
// Wharf cannot read proposes nothing.
func Detect(dir string) string {
	// INFO: composer.json first, for every rule: what a project requires says more
	// than a file that happens to sit in it.
	required := composerPackages(dir)
	for _, r := range rules {
		for _, p := range r.packages {
			if required[p] {
				return r.template
			}
		}
	}
	for _, r := range rules {
		for _, f := range r.files {
			if info, err := os.Stat(filepath.Join(dir, filepath.FromSlash(f))); err == nil && !info.IsDir() {
				return r.template
			}
		}
	}
	return ""
}

// composerPackages is every package composer.json requires, for production
// or development.
func composerPackages(dir string) map[string]bool {
	raw, err := os.ReadFile(filepath.Join(dir, "composer.json"))
	if err != nil {
		return nil
	}
	var c struct {
		Require    map[string]any `json:"require"`
		RequireDev map[string]any `json:"require-dev"`
	}
	if json.Unmarshal(raw, &c) != nil {
		return nil
	}
	out := map[string]bool{}
	for p := range c.Require {
		out[p] = true
	}
	for p := range c.RequireDev {
		out[p] = true
	}
	return out
}
