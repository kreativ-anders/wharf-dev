@v1
Feature: Webserver installation
  As a developer
  I want nginx and Apache to be there when I need them
  So that starting a project never ends at "not installed"

  One copy of each webserver serves every project. Each project gets its own
  generated config file, which the webserver's main config includes.

  Where a webserver comes from depends on what exists for the platform (see
  dev/architecture.md §4b): a download into "bin/", a package manager, or a
  copy already on the machine. The user sees one action either way.

  Background:
    Given the daemon is running

  Scenario: First start adopts a webserver already on the machine
    Given Apache is installed on the machine and nginx is not
    When the daemon starts for the first time
    Then Apache is used where it is, without being copied
    And Apache is selected as the default webserver

  Scenario: Installing nginx
    Given nginx is not installed
    When the user chooses "Install" next to nginx in Settings
    Then the newest nginx for this OS and CPU is installed
    And Settings shows it as installing until it is done
    And every project can be served by nginx without further setup

  Scenario: A webserver install fails
    Given the machine has no internet connection
    When the user installs nginx
    Then the GUI reports that it could not be fetched
    And no partial "bin/nginx" folder is left behind

  Scenario: Installing Apache on Linux
    Given Apache is not installed on Linux, and the distribution's package manager is apt, dnf, zypper or pacman
    When the user chooses "Install" next to Apache in Settings
    Then Wharf asks for the password once and installs the distribution's Apache package
    And the system's own Apache service is switched off, so it never holds port 80
    And Apache is used where the package put it

  Scenario: Declining the password prompt for a webserver install
    Given installing Apache needs the password
    When the user dismisses the password prompt
    Then Apache stays not installed, with no error shown
    And "Install" is still offered next to it

  Scenario: A webserver Wharf cannot install says how to get it
    Given there is no build of Apache that Wharf can install on this platform
    When the user opens Settings
    Then Apache offers no "Install" action
    And the GUI says how to install it instead

  Scenario: One webserver, one config file per project
    Given projects "my-kirby-site" and "other-site" are served by the global nginx
    When nginx starts
    Then one nginx process serves both projects
    And each project's server block is generated into its own file under "data/gen/nginx/"
    And the main nginx config includes exactly those files

  Scenario: A Kirby project needs no custom webserver config
    Given a Kirby site is a project with the "Kirby" config template
    When it is served by nginx or by Apache
    Then its pages, the Panel and its media are served
    And "content/", "site/", "kirby/" and dot-files are never served as files
    And PHP is handed the script's path as the OS spells it, a Windows drive letter included
    And no custom config is needed for any of it (see config-templates.feature)
