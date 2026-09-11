@v1
Feature: Quick-app scaffolding (PHP / Kirby)
  As a developer
  I want to scaffold a new PHP project with one click
  So that I don't have to set up folders, composer and a hosts entry by hand

  Background:
    Given the daemon is running
    And a "Kirby" quick-app template is registered

  Scenario: Scaffolding a new Kirby project
    When the user chooses "New project" → "Kirby" and enters the name "my-kirby-site"
    Then a folder "www/my-kirby-site" is created
    And the Kirby starter kit is fetched into that folder
    And a hosts entry for "my-kirby-site.wharf" is requested (see pretty-urls.feature)
    And the project appears in the GUI's project list with status "starting"

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
