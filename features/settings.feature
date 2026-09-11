@v1
Feature: Global settings
  As a developer
  I want a single settings surface for services and runtime versions
  So that configuration stays in one predictable place

  Background:
    Given the daemon is running
    And the settings screen reads directly from config/wharf.json

  Scenario: Changing the global active webserver
    When the user opens Settings and selects "apache" under "Webserver"
    Then the change is written to config/wharf.json
    And the behaviour matches service-management.feature's "Switching the active webserver" scenario

  Scenario: Adding a PHP version
    Given PHP "8.1" and "8.3" are offered
    And PHP "8.2" is present on the machine but not yet offered
    When the user adds PHP "8.2" via Settings → "Add runtime version"
    Then "8.2" is registered along with where it was found
    And "8.2" becomes selectable as a project-level override

    # Downloading a version that is not present is specified in
    # php-runtime.feature, "Downloading a PHP version that is not installed".

  Scenario: Settings screen hides roadmap services in v1
    Given "database" and "mail" are roadmap-only capabilities
    When the user opens Settings in the v1 build
    Then no "MySQL/PostgreSQL" or "Mailpit" section is shown
    And only "Appearance", "Webserver", "PHP runtime" and "SSL" sections are visible

  Scenario: Webserver status while nothing is running
    Given no project is running
    When the user opens Settings
    Then the active webserver is described as starting with the first project, not as "stopped"
    And each webserver lists the projects it serves

  Scenario: A webserver that is not installed says where it belongs
    Given the "apache" binary is not in "bin/apache/"
    When the user opens Settings
    Then "apache" is marked as not installed
    And the GUI shows where its binary is expected and can open that folder

  Scenario: Choosing light or dark appearance
    Given the appearance follows the system
    When the user selects "Dark" under "Appearance"
    Then the window switches to the dark theme at once
    And "appearance": "dark" is written to config/wharf.json
    And choosing "System" again removes the key, so the window follows the OS
