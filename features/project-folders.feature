@v1
Feature: Project folders
  As a developer
  I want to add a project from wherever its folder already is, and get back to it
  So that I never have to move my code or hunt for where it lives

  A folder is a project. Putting one in www/ is the default way in; a folder
  anywhere else on the machine can be added too, and stays where it is.

  Background:
    Given the daemon is running

  Scenario: Adding a folder from anywhere with the folder picker
    Given a folder "~/Code/Client Site" outside "www/"
    When the user chooses "Add folder…" and selects that folder
    Then a project "client-site" is registered
    And its files stay where they are
    And the project's config entry records the folder's location as "path"
    And its document root is that folder

  Scenario: Choosing a folder inside www/ registers it by name
    Given a folder "my-kirby-site" under "www/"
    When the user selects it in the folder picker
    Then it is registered exactly as if it had been added from the list of folders in "www/"
    And no "path" key is written

  Scenario: A folder whose name is already taken
    Given project "client-site" exists
    When the user selects another folder named "client-site" in the folder picker
    Then the GUI reports that the name is taken
    And nothing is registered

  Scenario: Removing a project added from elsewhere leaves its folder alone
    Given project "client-site" was added from "~/Code/Client Site"
    When the user removes the project from the GUI
    Then "~/Code/Client Site" and its files are untouched

  Scenario: Opening a project's folder
    Given project "my-kirby-site" exists
    When the user chooses "Open folder" for "my-kirby-site"
    Then the project's folder opens in the system file manager

  Scenario: Opening the www folder
    When the user chooses "Open www folder"
    Then "www/" opens in the system file manager
