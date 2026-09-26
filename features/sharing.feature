@v1
Feature: Sharing a project on the network
  As a developer
  I want to open a running project on my phone
  So that I can check it on a real device without deploying it

  A project is shared on a port of its own, at this machine's address on the
  local network, while the user wants it: "<name>.localhost" means the phone
  itself on the phone. The front door still answers this machine only on
  ports 80 and 443.

  A project with SSL is shared over HTTPS. Its certificate comes from Wharf's
  network certificate authority — not mkcert's, which this machine trusts for
  every name — so the phone trusts it once that authority is installed there.

  Background:
    Given the daemon is running
    And this machine's address on the local network is "192.168.1.23"

  Scenario: Sharing a running project on the network
    Given "my-kirby-site" is running without SSL
    When the user chooses "Share" for "my-kirby-site"
    Then the front door serves "my-kirby-site" to every device on the network at "http://192.168.1.23:8800"
    And the GUI shows that URL as a QR code and as text
    And PHP is told the host and port the phone used
    And "my-kirby-site.localhost" still answers this machine only

  Scenario: A shared project keeps its port
    Given "my-kirby-site" was shared on port 8800 before
    When it is shared again after Wharf restarted
    Then it is shared on port 8800 again, which wharf.json records as "lan_port"
    And if another program holds that port, it moves to the next free one and wharf.json records that

  Scenario: Sharing ends with the project
    Given "my-kirby-site" is shared
    When the user stops it, stops all or quits Wharf
    Then its port on the network is closed
    And starting it again does not share it again

  Scenario: Stopping sharing leaves the project running
    Given "my-kirby-site" is shared
    When sharing is stopped with "wharfctl share my-kirby-site off"
    Then its port on the network is closed
    And "my-kirby-site.localhost" is still served on this machine

  Scenario: Only a running project can be shared
    Given "my-kirby-site" is stopped
    When the user asks to share it
    Then it is not shared, and the GUI says to start it first

  Scenario: Sharing needs a local network
    Given this machine has no address on a private network
    When the user chooses "Share" for "my-kirby-site"
    Then it is not shared, and the GUI says to connect to a network first

  Scenario: A project on the other webserver is shared through the front door
    Given "legacy-app" is running and has "webserver_override: apache"
    When the user chooses "Share" for "legacy-app"
    Then the front door forwards its port on the network to "legacy-app"'s own apache
    And that apache tells PHP the port the phone used

  Scenario: Sharing a project with SSL over HTTPS
    Given "my-kirby-site" is running with SSL
    When the user chooses "Share" for "my-kirby-site"
    Then the front door serves it at "https://192.168.1.23:8800" with a certificate for "192.168.1.23"
    And that certificate comes from Wharf's network certificate authority
    And the authority's certificate — never its key — can be downloaded at "http://192.168.1.23/wharf-network-ca.crt"

  Scenario: The certificate step is shown only for a project with SSL
    When the user shares a project without SSL
    Then the GUI shows its URL alone
    When the user shares a project with SSL
    Then the GUI first offers the network certificate as a QR code, with its fingerprint to compare on the phone
    And says in a few words how to install it, the same for every phone

  Scenario: The network certificate authority cannot vouch for real websites
    When Wharf creates its network certificate authority
    Then it may sign certificates for private network addresses only, never for a domain name
    And it expires after one year
    And its private key stays in the Wharf folder, readable by the user alone

  Scenario: A new address needs no new certificate on the phone
    Given "my-kirby-site" is shared over HTTPS
    When this machine's address on the local network changes to "192.168.1.42"
    Then Wharf issues a certificate for "192.168.1.42" from the same authority
    And the front door serves "my-kirby-site" at "https://192.168.1.42:8800"
    And a phone that trusts the authority trusts the new certificate without installing anything

  Scenario: Replacing the network certificate authority
    Given a phone has the network certificate authority installed
    When the user chooses "Replace network certificate"
    Then a new authority is created and the old one's private key is deleted
    And the copy on the phone can no longer vouch for any certificate
    And every project shared over HTTPS is served with a certificate from the new authority

  Scenario: Resetting Wharf retires the network certificate authority
    Given the network certificate authority exists
    When the user resets Wharf
    Then its private key is deleted with it
