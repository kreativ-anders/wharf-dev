@v1
Feature: Tray icon actions
  As a developer
  I want to control projects and services from the tray
  So that I don't need to open the main window for routine actions

  Background:
    Given the application is running with a tray icon
    And at least one project exists

  Scenario: Starting a project from the tray
    When the user right-clicks the tray icon and selects a stopped project
    And chooses "Start"
    Then the daemon starts that project's required services
    And the tray menu updates the project's status to "running"

  Scenario: Stopping all services from the tray
    Given one or more services are running
    When the user selects "Stop all" from the tray menu
    Then the daemon stops every running service and project
    And the tray icon reflects an idle state

  Scenario: Adding a project via the tray
    When the user selects "Add project…" from the tray menu
    Then a folder picker is shown
    And the selected folder is added as a project without opening the main window
    And a folder outside "www/" stays where it is (see project-folders.feature)

  Scenario: Opening the main window
    When the user selects "Open Wharf" from the tray menu
    Then the main GUI window is shown
    And it reflects the same state as the tray menu
