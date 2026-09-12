@v1
Feature: Per-project (app-specific) configuration
  As a developer
  I want individual projects to deviate from global defaults
  So that one unusual project doesn't force a global settings change

  Background:
    Given the daemon is running
    And project "my-kirby-site" exists with no overrides set

  Scenario: Overriding the PHP version for one project
    Given the global default PHP version is "8.3"
    When the user sets "my-kirby-site" to PHP version "8.1"
    Then requests to "my-kirby-site" are served by the "8.1" PHP binary
    And all other projects continue to use PHP "8.3"

  Scenario: Enabling SSL for a single project
    Given "my-kirby-site" has "ssl: false"
    When the user enables SSL for "my-kirby-site" in the GUI
    Then a local certificate is generated for "my-kirby-site.localhost" via mkcert
    And "my-kirby-site" becomes reachable at "https://my-kirby-site.localhost"
    And other projects' SSL settings are unaffected

  Scenario: Removing an override reverts to the global default
    Given "my-kirby-site" has "webserver_override: apache"
    When the user picks the globally active webserver for it in the GUI
    Then "my-kirby-site" is served by whichever webserver is globally active
    And the "webserver_override" key is removed from the project's config entry

  Scenario: Every project row leads to its settings
    When the user looks at the project list
    Then each project offers a "Project settings" action
    And it opens the project's PHP version, webserver, SSL, custom config and folder

  Scenario: A running project shows what serves it
    Given "my-kirby-site" is running on nginx "1.27.3" with PHP "8.3.14"
    When the user looks at the project list
    Then its row shows "nginx 1.27.3 · PHP 8.3.14" beneath its URL
    And a project with a PHP override shows the version of its own PHP build
    And a stopped project's row shows no versions

  Scenario: Custom webserver directives for one project
    When the user creates a custom nginx config for "my-kirby-site"
    Then "config/vhosts/my-kirby-site.nginx.conf" is created with a commented starting point
    And its contents are included in "my-kirby-site"'s nginx server block
    And no other project's server block includes it

  Scenario: Each webserver keeps its own custom config
    Given "my-kirby-site" has a custom nginx config and a custom apache config
    When "my-kirby-site" is served by "apache"
    Then only the apache config is included
    And switching it back to "nginx" includes only the nginx config

  Scenario: Saving a custom config applies it
    Given "my-kirby-site" is running with a custom nginx config
    When the user saves a change to that file
    Then the webserver serving "my-kirby-site" is restarted with the change

  Scenario: A custom config the webserver refuses names the problem
    Given "my-kirby-site" is running with a custom nginx config
    When the user saves a change that nginx refuses to start with
    Then "my-kirby-site" shows nginx's own error, naming the file and line
    And the error appears as soon as nginx exits, not after a timeout
