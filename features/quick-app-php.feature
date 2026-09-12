@v1
Feature: Quick-app scaffolding (PHP / Kirby)
  As a developer
  I want to scaffold a new PHP project with one click
  So that I don't have to set up folders, composer and a hosts entry by hand

  Background:
    Given the daemon is running
    And a "Kirby" quick-app template is registered

  Scenario: Scaffolding a new Kirby project
    When the user chooses "New project", enters the name "my-kirby-site" and picks the "Kirby" template
    Then a folder "www/my-kirby-site" is created
    And the Kirby starter kit is fetched into that folder
    And the project appears in the GUI's project list with status "starting"
    And it is reachable at "http://my-kirby-site.localhost" (see pretty-urls.feature)

  Scenario: Choosing the webserver while creating a project
    Given the global active webserver is "nginx"
    When the user creates "legacy-app" and picks "apache" as its webserver
    Then "legacy-app" is created with "webserver_override: apache"
    And its settings open, with its apache config one click away
    And a project created with the globally active webserver picked has no override

  Scenario: Creating an empty project
    When the user creates "blank" with the "Empty folder" template
    Then a folder "www/blank" is created with an "index.php" in it
    And nothing is downloaded

  Scenario: Scaffolding fails without network access
    Given the machine has no internet connection
    When the user attempts to scaffold a new "Kirby" project
    Then the GUI reports that the template could not be fetched
    And no partial project folder is left behind

  @roadmap
  Scenario: Additional quick-app templates
    Given templates for "WordPress" and "Laravel" are registered
    When the user selects one of these templates
    Then the equivalent scaffolding flow runs as for "Kirby"
