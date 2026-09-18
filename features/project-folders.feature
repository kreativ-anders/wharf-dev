@v1
Feature: Project folders
  As a developer
  I want to add a project from wherever its folder already is, and get back to it
  So that I never have to move my code or hunt for where it lives

  A folder is a project. "Add project…" is the one way in: pick a folder,
  confirm what Wharf detected, and it runs. The folder stays where it is.
  A folder put in www/ is found by name and added the same way.

  Background:
    Given the daemon is running

  Scenario: Before the first project, Wharf says what is missing
    Given no project has been added yet
    And no PHP and no webserver are installed
    When the user opens Wharf
    Then the window says PHP is missing and offers to download the recommended version
    And it says no webserver is installed and offers to install each one Wharf can install
    And a webserver Wharf cannot install says how to get it instead
    And each line says in words whether it is ready, not by colour alone
    And "Add project…" is offered below them

  Scenario: Once PHP and a webserver are there, the window says so
    Given no project has been added yet
    And PHP "8.4" and nginx are installed
    When the user opens Wharf
    Then the window shows both as ready
    And "Add project…" is the one thing left to do

  Scenario: Adding a project with the folder picker
    Given a folder "~/Code/Client Site" outside "www/"
    When the user chooses "Add project…" and selects that folder
    Then a sheet proposes the name "client-site" and shows its address "http://client-site.localhost"
    And it proposes the config template detected in the folder and the active webserver
    When the user chooses "Add"
    Then a project "client-site" is registered and started
    And its files stay where they are
    And the project's config entry records the folder's location as "path"
    And its document root is that folder

  Scenario: The proposed name comes from the folder name
    When the user selects a folder in the folder picker
    Then its name is rewritten into a project name that works as a folder and a hostname:
      | folder               | project name       |
      | My Kirby Site        | my-kirby-site      |
      | Müller & Söhne       | mueller-soehne     |
      | Café_Relaunch 2026   | cafe-relaunch-2026 |
      | Straße.de            | strasse-de         |
      | --Hello   World--    | hello-world        |
      | x                    | x                  |
    And the name field holds the name alone, the address shows it with ".localhost"
    And a name the user types gets the same rewrite once they leave the field
    And a name with no letter or digit in it is refused, asking for at least one
    And a name sent to the daemon without the GUI gets the same rewrite

  Scenario Outline: The config template is detected from the folder
    Given a folder containing <marker>
    When the user selects it in the folder picker
    Then the sheet proposes the "<template>" config template
    And the user can pick another one, or "None", before adding

    # Checked in this order, most specific first: Statamic is a Laravel
    # app, and Laravel and Drupal both pull in Symfony components.
    Examples:
      | marker                                             | template  |
      | "statamic/cms" in composer.json                    | Statamic  |
      | "craftcms/cms" in composer.json                    | Craft CMS |
      | "drupal/core-recommended" in composer.json         | Drupal    |
      | web/core/lib/Drupal.php                            | Drupal    |
      | "laravel/framework" in composer.json               | Laravel   |
      | an "artisan" file                                  | Laravel   |
      | "symfony/framework-bundle" in composer.json        | Symfony   |
      | wp-config.php                                      | WordPress |
      | wp-includes/version.php                            | WordPress |
      | "roots/wordpress" in composer.json                 | WordPress |
      | "getkirby/cms" in composer.json                    | Kirby     |
      | kirby/bootstrap.php                                | Kirby     |
      | only an index.php                                  | None      |

  Scenario: Choosing the webserver while adding a project
    Given the global active webserver is "nginx"
    When the user adds "legacy-app" and picks "apache" as its webserver
    Then "legacy-app" is registered with "webserver_override: apache"
    And a project added with the globally active webserver picked has no override

  Scenario: Adding a project for a webserver that is not installed
    Given nginx is not installed
    When the user picks nginx in the "Add project…" sheet
    Then the sheet says nginx is not installed and offers to install it

  Scenario: Choosing a folder inside www/ registers it by name
    Given a folder "my-kirby-site" under "www/"
    When the user selects it in the folder picker
    Then the sheet proposes "my-kirby-site", and the name cannot be changed: it is the folder's
    And after "Add" no "path" key is written

  Scenario: Adding a folder found in www/
    Given a folder "blog" under "www/" that is not a project yet
    When the user chooses "Add" next to "blog" in the list
    Then the same sheet opens as for a folder chosen in the folder picker

  Scenario: A folder whose name is already taken
    Given project "client-site" exists
    When the user adds another folder named "client-site"
    Then the sheet says the name is taken and stays open
    And nothing is registered until the user picks another name

  Scenario: Choosing a folder that is already a project
    Given "~/Code/Client Site" is the project "client-site"
    When the user selects that folder in the folder picker
    Then no sheet opens and nothing is registered
    And the settings of "client-site" open instead

  Scenario: Removing a project added from elsewhere leaves its folder alone
    Given project "client-site" was added from "~/Code/Client Site"
    When the user removes the project from the GUI
    Then "~/Code/Client Site" and its files are untouched

  Scenario: Opening a project's folder
    Given project "my-kirby-site" exists
    When the user chooses "Open folder" for "my-kirby-site"
    Then the project's folder opens in the system file manager

