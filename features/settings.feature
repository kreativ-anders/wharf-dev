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
    Then no "MySQL/PostgreSQL" or "Mailpit" page is listed
    And only the "General", "Webserver", "PHP" and "SSL" pages are listed

  Scenario: Settings pages are chosen from a side navigation
    When the user opens Settings
    Then a navigation on the left lists the settings pages
    And choosing a page shows that page alone
    And "General" holds the appearance, the config folder, the version and "Reset Wharf…"

  Scenario: General shows the version, and no update check yet
    When the user opens Settings on "General"
    Then the version of the running Wharf is shown, as the daemon reports it
    And "Check for updates" is shown but cannot be pressed
    And a note says Wharf does not check for updates yet, and will only ever look when asked

  # The update check itself, fixed ahead of time so its constraints survive
  # until it is built. Wharf has no published releases to check against yet.
  @roadmap
  Scenario: Checking for updates on request
    Given Wharf publishes its releases on GitHub
    When the user presses "Check for updates" under "General"
    Then the daemon asks the public releases API for the newest version and compares it with its own
    And nothing is looked up at start or in the background — only on this press
    And a failed lookup, offline or rate-limited, says to try again later instead of raising an error
    And a newer release is offered as a download, saved only once it matches the release's SHA256SUMS
    And Wharf never runs or installs what it downloaded

  Scenario: Webserver status while nothing is running
    Given no project is running
    When the user opens Settings
    Then the active webserver is described as starting with the first project, not as "stopped"
    And each webserver shows its name and version, not where it was installed from
    And the projects a webserver serves are named when the pointer rests on it, not in the list

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

  Scenario: Resetting Wharf
    Given projects exist in "www/" and one was added from elsewhere
    When the user chooses "Reset Wharf…" in Settings and confirms
    Then every service is stopped
    And every folder in "www/" is deleted
    And a folder added from elsewhere is unregistered but left where it is
    And everything in "config/" is deleted: settings, custom webserver configs and PHP settings
    And project certificates, generated configs and service logs are deleted
    And wharf.json is back to what a first start writes
    And downloaded PHP versions and webservers are kept

  Scenario: Reset asks first
    When the user chooses "Reset Wharf…" in Settings
    Then a warning names every folder that will be deleted and every folder that is kept
    And nothing is deleted unless the user confirms
