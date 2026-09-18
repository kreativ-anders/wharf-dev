package project

import (
	"os"
	"path/filepath"
	"testing"
)

// features/project-folders.feature — "The config template is detected from the
// folder"
func TestTheConfigTemplateIsDetectedFromTheFolder(t *testing.T) {
	composer := func(pkg string) map[string]string {
		return map[string]string{"composer.json": `{"require": {"php": "^8.2", "` + pkg + `": "*"}}`}
	}
	for _, c := range []struct {
		name  string
		files map[string]string
		want  string
	}{
		{"statamic/cms in composer.json", composer("statamic/cms"), "statamic"},
		{"craftcms/cms in composer.json", composer("craftcms/cms"), "craft"},
		{"drupal/core-recommended in composer.json", composer("drupal/core-recommended"), "drupal"},
		{"web/core/lib/Drupal.php", map[string]string{"web/core/lib/Drupal.php": "<?php"}, "drupal"},
		{"laravel/framework in composer.json", composer("laravel/framework"), "laravel"},
		{"an artisan file", map[string]string{"artisan": "#!/usr/bin/env php"}, "laravel"},
		{"symfony/framework-bundle in composer.json", composer("symfony/framework-bundle"), "symfony"},
		{"wp-config.php", map[string]string{"wp-config.php": "<?php"}, "wordpress"},
		{"wp-includes/version.php", map[string]string{"wp-includes/version.php": "<?php"}, "wordpress"},
		{"roots/wordpress in composer.json", composer("roots/wordpress"), "wordpress"},
		{"getkirby/cms in composer.json", composer("getkirby/cms"), "kirby"},
		{"kirby/bootstrap.php", map[string]string{"kirby/bootstrap.php": "<?php"}, "kirby"},
		{"only an index.php", map[string]string{"index.php": "<?php"}, ""},

		// INFO: The order matters: each of these also matches a more general rule.
		{"Statamic ships an artisan file", map[string]string{
			"composer.json": `{"require": {"statamic/cms": "*", "laravel/framework": "*"}}`,
			"artisan":       "#!/usr/bin/env php",
		}, "statamic"},
		{"Drupal requires Symfony components", map[string]string{
			"composer.json": `{"require": {"drupal/core": "*", "symfony/framework-bundle": "*"}}`,
		}, "drupal"},
		{"composer.json outweighs a stray file", map[string]string{
			"composer.json": `{"require": {"getkirby/cms": "*"}}`,
			"bin/console":   "#!/usr/bin/env php",
		}, "kirby"},
		{"a require-dev package counts", map[string]string{
			"composer.json": `{"require-dev": {"laravel/framework": "*"}}`,
		}, "laravel"},
		{"a broken composer.json is ignored", map[string]string{
			"composer.json":       `{"require": `,
			"kirby/bootstrap.php": "<?php",
		}, "kirby"},
		{"a folder named like a marker file is not one", map[string]string{"artisan/readme.txt": "x"}, ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			for name, body := range c.files {
				path := filepath.Join(dir, filepath.FromSlash(name))
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			if got := Detect(dir); got != c.want {
				t.Errorf("Detect = %q, want %q", got, c.want)
			}
		})
	}

	if got := Detect(filepath.Join(t.TempDir(), "missing")); got != "" {
		t.Errorf("a missing folder proposed %q", got)
	}
}
