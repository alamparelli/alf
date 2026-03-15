---
category: Configuration
tags: setup, wizard, onboarding, first run
order: 1
---

# Setup Wizard

The Setup Wizard walks you through configuring ALF from the Control Center. No command line needed.

## When does it appear?

The wizard shows automatically on first login. You can also re-run it from **Settings** at any time.

## Steps

The wizard has 4 steps:

| Step | What you do |
|------|-------------|
| **1. Choose your AI backend** | Pick how ALF connects to AI models: Claude CLI (default), OpenRouter, OpenAI, or Ollama. Enter your API key if needed. |
| **2. Claude authentication** | Only shown if you picked Claude CLI. Confirms that Claude is authenticated inside the container. |
| **3. Telegram (optional)** | Connect a Telegram bot so you can chat with ALF from your phone. Skip this if you only want to use the Control Center. |
| **4. Pick a tier preset** | Choose a pre-built configuration for how ALF routes messages to different models. You can customize later. |

After completing the wizard, ALF is ready to use.

## What are presets?

Presets are ready-made tier configurations. Each preset sets up which models ALF uses and when.

| Preset | What it does |
|--------|-------------|
| **Claude Default** | Full Claude stack (Haiku, Sonnet, Opus) with smart routing |
| **Claude Standard** | Balanced setup with fewer tiers |
| **OpenRouter Standard** | Uses OpenRouter models with routing |

You can always customize tiers later in the **Tiers** tab. Presets are just a starting point.

## Re-running the wizard

Go to **Settings** and click **Run Setup Wizard**. All fields are pre-filled with your current configuration so you can adjust without starting from scratch.

## What's next?

- [Getting Started](docs:getting-started) - overview of all ALF features
- [Setting Up Tiers](docs:tier-setup) - customize which models ALF uses
- [Backends & Models](docs:backends) - connect additional AI providers
