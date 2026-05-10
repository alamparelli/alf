---
category: Features
tags: marketplace, apps, install, update
order: 6
---

# Marketplace

The marketplace is ALF's app store. Browse, install, and manage apps that extend what ALF can do -- from web-based tools in the Control Center to background services and CLI utilities.

## Browsing apps

Open the **Marketplace** tab in the sidebar. Apps are grouped by category and sorted alphabetically within each group.

Each app card shows:

- **Name and icon**
- **Version** and **author**
- **Description** -- what the app does
- **Tools** -- any tool functions the app exposes to ALF
- **Status badge** -- Available, Installed, Enabled, or Disabled

Click **Refresh** to re-fetch the catalog from the registry.

## Installing an app

1. Find the app in the marketplace.
2. Click **Install**. ALF downloads the app source from the registry, verifies it, and places it in the apps directory.
3. After installation the app moves to **Installed** state. Click **Enable** to activate it.

Once enabled, the app's tools become available to ALF and its UI (if any) appears in the sidebar under **APPS**.

## Enabling and disabling

- **Enable** -- activates the app. Its tools are registered and its UI becomes accessible.
- **Disable** -- deactivates the app without removing it. The app stays on disk but its tools are unregistered and its sidebar entry is hidden.

Use disable when you want to temporarily turn off an app without losing its data or configuration.

## Updating apps

When a newer version is available in the registry, an **Update** button appears on the app card with the new version number. Click it to pull the latest version.

You can also check for updates manually by clicking **Refresh** in the toolbar. A badge appears on the Marketplace sidebar item when updates are available.

## Where apps appear

Enabled apps with a web UI show up in the sidebar under the **APPS** section. Each app gets its own entry with its icon and name. Clicking it loads the app inside the Control Center at `alf://apps/<slug>`.

Apps without a web UI (services, CLI tools) do not appear in the sidebar but their tools are still available to ALF during conversations.

## App types

| Type | Description |
|------|-------------|
| **Web UI app** | Has an `index.html` served inside the Control Center. Appears in the sidebar. |
| **Service app** | Runs a background process that provides tools to ALF. No visible UI. |
| **CLI tool** | Exposes command-line tools that ALF can invoke during prompts. No visible UI. |

An app can combine types -- for example, a service with a web dashboard.

## Uninstalling

1. Find the installed app in the marketplace.
2. Click **Uninstall**. A confirmation dialog appears warning that all app data will be removed.
3. Confirm to remove the app completely.

After uninstalling, the app reverts to **Available** in the catalog and can be reinstalled at any time.

> Apps you published yourself (marked with a "Your app" badge) cannot be uninstalled or disabled from the marketplace UI.

## Security

All marketplace apps are **source-only** -- no pre-compiled binaries. Source code is auditable before and after installation. Apps are compiled locally inside the container at install time.

Marketplace bundles ship pre-signed by the publisher. To install from a publisher you haven't used before, an admin adds their key once with `alf trust add <publisher-key.pub>`. After that, every bundle from that publisher loads automatically. See [Isolation Model](docs:isolation-model) for the trust model.

## For developers

This guide covers using the marketplace as an end user. If you want to create and publish your own apps, see [Building Marketplace Apps](marketplace-apps.md) for the developer documentation covering app structure, manifest format, SDK usage, and publishing to the registry.

## What's next

- [Building Marketplace Apps](marketplace-apps.md) -- create and publish your own apps
- [System Tools](system-tools.md) -- built-in tools available without the marketplace
