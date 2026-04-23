Hey everyone. [smile]

In the next few minutes, you'll have ALF running on your own infrastructure. [pause]

Before we start, here's what you'll need: a VPS, a domain name, Docker Desktop if you're on macOS, and access to a compatible model. Claude, Codex, or OpenRouter all work fine. For the sake of this video, I'll skip the domain registration, the VPS purchase, the Docker Desktop setup, and how to subscribe to your favorite LLM provider. [pause]

If you also want to hook up a Telegram bot, no problem. We'll get to that later during onboarding. Right now, let's focus on getting a clean install done. [nod]

Alright, let's jump in. Head over to alfos.ai. Scroll down to the bottom of the homepage and you'll see the install button. Go ahead and copy that command. [pause]

Now open your terminal, either on your VPS or locally, paste the command, and hit enter.

From there, the binary downloads and installs itself. [pause]

Depending on your setup, you might need to restart your terminal or run the `source` command it suggests. That's totally normal, nothing to worry about. [smile]

Once that's out of the way, run `alf init`. [pause]

The first thing it does is check for Docker. On a fresh VPS, it'll install Docker for you automatically. On Mac though, you'll need Docker Desktop already installed, so make sure that's sorted before moving on. [pause]

After that, it asks where you want to install everything. Honestly, just go with the default.

Then comes the Control Center, which is ALF's web interface. [pause]

Here you get to decide if you want to expose it through your domain. I'd recommend doing that. [nod]

If you go that route, enable HTTPS, drop in your domain, and add your email for Let's Encrypt.

Moving on, pick your time zone. Whatever works for you. I'll go with Europe/Paris. [pause]

The next screen asks if you want to connect an external directory to the Docker folder. For a standard install, leave it on the default.

Same goes for the runtime. Default is fine. [pause]

And now the real installation begins. [smile]

ALF pulls down everything it needs: Whisper, Embed, and the main container. [pause]

Once that's done, it generates a Magic Link that stays valid for an hour.

That link is what gets you into your environment through the browser. [pause]

So copy it, open up the Control Center, and click Login.

From there, the visual onboarding takes over. You pick your provider. Let's say Codex from OpenAI. Select it, sign in, and keep going. [pause]

This is also where you can connect Telegram if you want to chat with ALF that way. Or skip it, up to you.

Right after that, pick your default profile. The recommended one is usually what you want. [pause]

Then you hit the summary screen and the Vault password.

The Vault is ALF's secure space. It's where your secrets, credentials, and authentications live, all protected. [nod]

Pause the video if you want. Make sure to pick something strong. [pause]

Once you're happy with everything, click Apply & Start. [smile]

From here, you can start chatting. Type "Hi" to kick off your first interaction.

That's it, your ALF is installed. [smile]

If you got stuck somewhere, drop a comment and I'll help you out. See you in the next video where we'll cover the first use. [wave]
