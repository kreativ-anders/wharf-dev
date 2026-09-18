@v1
Feature: Config templates
  As a developer
  I want the webserver rules of the CMS and frameworks I use kept in one place
  So that a Laravel or WordPress project runs without writing nginx or Apache rules by hand

  A config template is a whole nginx server block and a whole Apache virtual
  host, the way a CMS's docs show them, PHP handler included. A project picks
  one. Wharf fills in the placeholders — {{listen}}, {{ssl}},
  {{server_name}}, {{root}}, {{log_dir}}, {{php}} and {{fastcgi}}, the
  values only Wharf knows — and uses every other line as written. A project
  that needs rules of its own gets a custom config: a copy of its template
  with the same placeholders (app-configuration.feature).

  Scenario: A config template is a whole server block, with placeholders for what only Wharf knows
    Given "my-kirby-site" uses the "Kirby" config template with SSL on
    When "my-kirby-site" is served by nginx
    Then its generated file holds the template's server block once for port 80 and once for port 443
    And every placeholder is filled in: its ports, certificate, host name, folder, logs and PHP
    And the template's own PHP location is used as written
    And rules that leave out {{listen}}, {{ssl}} or {{server_name}} are refused on save, naming what is missing

  Scenario: A project without a config template gets the webserver's defaults
    Given "my-site" has no config template
    When "my-site" is served by nginx
    Then its server block serves the project folder and runs .php files with its PHP, and rewrites nothing
    And served by Apache, the .htaccess files in the project folder apply
    And no "template" key is written to "my-site"'s entry in config/wharf.json

  Scenario: Built-in config templates for common CMS and frameworks
    When the user opens Settings on "Webserver"
    Then "Config templates" lists Kirby, Laravel, WordPress, Statamic, Symfony, Craft CMS and Drupal
    And each has rules for nginx and for Apache

  Scenario: Picking a config template for a project
    When the user picks "Laravel" as the config template in "my-app"'s settings
    Then "template": "laravel" is written to "my-app"'s entry in config/wharf.json
    And "my-app"'s server block serves "public/" in its folder with Laravel's rules
    And a running "my-app" is restarted with them
    And picking "None" removes the key

  Scenario: Editing a config template in Wharf
    When the user chooses "Edit" on a config template
    Then a window shows its nginx server block or its Apache virtual host, one at a time, with line numbers
    And saving writes "config/templates/<id>.<webserver>.conf"
    And every running project using the template is restarted with the change

  Scenario: Changing a built-in config template, and restoring it
    Given the user saved a change to the built-in "WordPress" config template
    Then Settings marks "WordPress" as changed
    And projects using it get the changed rules
    When the user chooses "Restore" on "WordPress" and confirms
    Then its files in "config/templates/" are deleted and Wharf's own rules apply again

  Scenario: Creating a config template
    When the user chooses "New template…" and enters the name "My API"
    Then a config template "my-api" is created, starting from the block a project without a template gets
    And the editor opens on it
    And a name another config template already has is refused

  Scenario: Deleting a config template
    Given the user created the config template "my-api"
    When the user deletes it and confirms
    Then its files are deleted from "config/templates/"
    But a config template a project still uses is not deleted, and the message names those projects

  Scenario: A config template edited in another editor applies too
    Given "my-app" is running with the "laravel" config template
    When the user saves "config/templates/laravel.nginx.conf" in another editor
    Then the webserver serving "my-app" is restarted with the change

  Scenario: A project naming a config template that does not exist says so
    Given "my-app" names the config template "gone" in config/wharf.json
    When the user starts "my-app"
    Then "my-app" shows that the config template "gone" does not exist and how to pick another
