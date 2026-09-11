@v1
Feature: Pretty URLs via hosts file
  As a developer
  I want each project reachable under a friendly local domain
  So that I don't need to remember ports

  Background:
    Given the daemon is running
    And the daemon has access to the OS hosts file path
      | OS      | path                                             |
      | Windows | C:\Windows\System32\drivers\etc\hosts            |
      | macOS   | /etc/hosts                                       |
      | Linux   | /etc/hosts                                       |

  Scenario: Creating a project registers a hosts entry
    Given a new folder "my-kirby-site" is added under "www/"
    When the user adds "my-kirby-site" as a project in the GUI
    Then an elevation prompt is shown via RequestElevatedWrite
    And, once approved, a hosts entry "127.0.0.1 my-kirby-site.wharf" is written
    And the project is reachable at "http://my-kirby-site.wharf"

  Scenario: Elevation is declined
    Given the user is adding a project
    When the elevation prompt is declined
    Then no hosts entry is written
    And the project remains reachable only via its raw port
    And the GUI shows the fallback URL instead of the pretty URL

  Scenario: Removing a project removes its hosts entry
    Given project "my-kirby-site" has an existing hosts entry
    When the user removes the project from the GUI
    Then the corresponding hosts entry is deleted
    And no other project's hosts entries are affected

  @roadmap
  Scenario: Wildcard resolution without per-project hosts entries
    Given a wildcard domain "*.wharf.test" is configured
    When a new project is added
    Then no hosts-file write is required
    And the project is immediately reachable under "<project>.wharf.test"
