@v1
Feature: PHP runtime detection and selection
  As a developer
  I want the tool to use the PHP I already have and let me pick a version
  So that it is usable on first launch without a download step

  PHP has no formal LTS track: each minor version gets roughly two years of
  active support, then two years of security fixes. "Latest LTS" here means
  the newest version still in active support (see daemon/internal/php).

  Background:
    Given the daemon has not been started before, so no config file exists

  Scenario: First start adopts the PHP versions already on the machine
    Given PHP "8.1" and "8.4" are installed on the machine
    When the daemon starts for the first time
    Then both "8.1" and "8.4" are offered as runtime versions
    And "8.4" is selected as the global default, being the newest in active support
    And the location of each adopted version is recorded in config/wharf.json
    And no PHP binary is downloaded

  Scenario: First start prefers a supported version over a newer unsupported one
    Given only end-of-life and security-only PHP versions are installed
    When the daemon starts for the first time
    Then the newest version still receiving security fixes is selected
    And the version's support status is shown alongside it in the picker

  Scenario: First start with no PHP installed
    Given no PHP installation can be found on the machine
    When the daemon starts for the first time
    Then the newest actively supported PHP version is selected as the default
    And no runtime versions are offered as installed
    And starting a project reports where the missing binary is expected
    And the picker offers to download the selected version

  Scenario: Choosing a different PHP version globally
    Given PHP "8.3" and "8.4" are installed and "8.4" is the global default
    When the user selects "8.3" in the PHP runtime picker
    Then "8.3" is written to config/wharf.json as the global default
    And projects without their own PHP override are served by "8.3"
    And projects with an override keep the version they had

  Scenario: Re-scanning after installing a PHP version
    Given the daemon is running and PHP "8.4" was not installed at start
    When the user installs PHP "8.4" and asks the GUI to re-scan
    Then "8.4" appears in the picker without restarting the daemon

  Scenario: Downloading a PHP version that is not installed
    Given PHP "8.4" is not installed on the machine
    When the user chooses "Download" next to PHP "8.4" in Settings
    Then the newest "8.4" build for this OS and CPU is downloaded into "bin/php/8.4"
    And the picker shows it as downloading until it is done
    And it becomes selectable without any further setup

  Scenario: A PHP download fails
    Given the machine has no internet connection
    When the user downloads PHP "8.4"
    Then the GUI reports that the build could not be fetched
    And no partial "bin/php/8.4" folder is left behind

  Scenario: Only supported versions are offered for download
    When the user opens Settings
    Then every PHP version still receiving security fixes that is not installed is offered for download
    And end-of-life versions are not offered

  Scenario: Opening where a PHP version lives
    Given PHP "8.4" is installed
    When the user chooses "Open folder" next to it in the picker
    Then the folder holding its binaries opens in the system file manager
