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

  Scenario: Stopping a project
    Given a project is running
    When the user chooses "Stop" for it, in the tray menu or the project list
    Then the daemon stops the process serving that project
    And the project's status updates to "stopped"

  Scenario: Restarting a project
    Given a project is running, or failed to start
    When the user chooses "Restart" for it, in the tray menu or the project list
    Then its webserver config is generated again
    And the process serving it is restarted with that config
    And its PHP backend is started if it is not running

  Scenario: Every project offers the actions that fit its state
    When the user looks at a project in the project list or the tray menu
    Then a running project offers "Stop" and "Restart"
    And a stopped project offers "Start"
    And a project that failed to start offers "Start" and "Restart"

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
