# Wharf

A local PHP development environment for macOS, Windows and Linux. It runs
PHP, nginx and Apache right on your computer, without Docker or a VM. Every
folder is a project with its own address, like `http://my-site.localhost`,
HTTPS in one switch, and the PHP version it needs.

More at **[getwharf.dev](https://getwharf.dev)**.

## Download

From the [latest release](https://github.com/kreativ-anders/wharf-dev/releases/latest):

| System | File |
|---|---|
| macOS 12 or newer, Apple silicon and Intel | `Wharf-<version>-mac.dmg` |
| Windows 10 or 11, x64 | `Wharf-<version>-windows-x64-setup.exe` |
| Linux, x64 | `Wharf-<version>-linux-x64.AppImage` |

`SHA256SUMS` in the same release lets you check a download.

## Install

**macOS:** open the DMG and drag Wharf into Applications.

**Windows:** run the installer. It is not code-signed, so SmartScreen warns
once: choose *More info*, then *Run anyway*.

**Linux:** make the AppImage executable and start it:

```sh
chmod +x Wharf-*-linux-x64.AppImage
./Wharf-*-linux-x64.AppImage
```

Wharf asks for your password once before the first project starts, to use
ports 80 and 443.

## First steps

1. Start Wharf. If PHP or a webserver is missing, the window says so and
   installs it when you ask.
2. Choose **Add project…** and pick a folder. It stays where it is.
3. Choose **Open**. The project is at `http://<name>.localhost`.

Everything Wharf keeps is in `~/Wharf`: `www/` for projects, `config/` for
the settings, `data/log/` for the logs.

## Contributing

[CONTRIBUTING.md](CONTRIBUTING.md) explains the setup; [CLAUDE.md](CLAUDE.md)
holds the rules and a map of the repository. Bugs and questions go to the
[issues](https://github.com/kreativ-anders/wharf-dev/issues).

## Licence

MIT, see [LICENSE](LICENSE).
