# cmdg - A command line client to GMail

Copyright Thomas Habets <thomas@habets.se> 2015-2021

https://github.com/ThomasHabets/cmdg

## License

This software is dual-licensed GPL and "Thomas is allowed to release a
binary version that adds shared API keys and nothing else".

## Introduction

cmdg is a commandline client to GMail that provides a UI more similar
to Pine/Alpine.

It uses the GMail API to interact with your mailbox. This has several
benefits.

### Benefits over IMAP
* No passwords stored on disk. (application-specific passwords are
  also passwords, and can be used for more than GMail). OAuth2 is used
  instead, and cmdgs access can be revoked
  [here](https://security.google.com/settings/security/permissions).
  cmdg can only access your GMail and contacts, and cannot lose your password
  even if the machine it runs on gets hacked.
* The "labels" model is native in the cmdg UI, unlike IMAP clients
  that try to map GMail labels onto IMAP.
* Contacts are taken from your Google contacts, your Google "Other
  contacts" (people you have emailed), the senders and recipients of your
  2000 newest messages, and the recipients of your 5000 newest sent
  messages. The messages are scanned in the background after startup,
  which takes a few minutes the first time; suggestions update as it goes.
  Recipient suggestions list the people in those messages first, most
  recently emailed first, then everyone else alphabetically
* TODO: other benefits, I'm sure.

### Benefits over the GMail web UI
* Emacs-ish keys. If there's a need the key mapping can be made
  configurable.
* Uses a real $EDITOR.
* Really fast. No browser, CSS, or javascript getting in the way.
* Proper quoting. The GMail web UI encourages top-posting. Ugh.
* It uses 100% keyboard navigation. GMail web UI has very good
  keyboard navigation for a web app, but still requires mouse for
  a few things.
* cmdg works without a graphic terminal.
* cmdg uses less bandwidth (citation needed), and much less memory.
* Local GPG integration. There are currently no *good* ways to
  integrate GPG with the GMail web UI.

### A security difference
* GMail web UI uses username and password to log in, which means they
  can be stolen. You should be using [U2F
  Yubikeys](https://www.yubico.com/products/yubikey-hardware/fido-u2f-security-key/),
  so that losing the password isn't as big of a deal. The user has to
  re-type the password every now and then, which is an opportunity for
  the attacker to steal the password.
* OAuth token in cmdg.conf can be copied, and the thief would be
  able to access the users GMail until the key is revoked. The
  access does not expire on its own.

## Installing

### Building manually

```
$ go build ./cmd/cmdg
$ sudo cp cmdg /usr/local/bin
```

### Homebrew (maintained by separate author)

```
brew tap cutzenfriend/homebrew-cmdg
brew install cmdg
```

## Configuring
You need to configure `cmdg` in order to provide it with authentication
so it can talk to GMail on your behalf. To do this you'll need to generate
a ClientID and ClientSecret. You can do this with the following steps:

  1. Go to the [Google Developers Console](https://console.developers.google.com/apis).
  1. Select an existing project or create a new project
  1. Visit the following URLs and ensure that these three APIs are enabled.
     1. Gmail - `https://console.developers.google.com/apis/api/gmail.googleapis.com/overview`
     1. Google Drive API `https://console.developers.google.com/apis/api/drive.googleapis.com/overview`
     1. People API - `https://console.developers.google.com/apis/api/people.googleapis.com/overview`
  1. Navigate to the "Google Auth Platform" page (called "OAuth consent
     screen" in older versions of the console) and fill in the app name and
     support email under "Branding".
  1. Under "Audience", choose "External". Either leave the app in "Testing"
     and add your Google account as a test user, or click "Publish app". A
     published app keeps working without being verified, at the cost of a
     "Google hasn't verified this app" warning when you sign in. Google's
     documentation says a sign-in to an app in "Testing" expires after 7
     days, after which you would have to rerun `cmdg -configure`; with an
     enterprise-managed Google account it has been seen to last for months
     instead.
  1. Leave the scope list under "Data Access" empty. `cmdg` asks for the
     scopes it needs when you sign in, and listing sensitive scopes there
     makes the console ask you to submit the app for verification, which a
     personal app does not need. The scopes it asks for are:
     1. Gmail API - `https://www.googleapis.com/auth/gmail.modify`
     1. Google Drive API - `https://www.googleapis.com/auth/drive.appdata`
     1. People API - `https://www.googleapis.com/auth/contacts`
     1. People API - `https://www.googleapis.com/auth/contacts.other.readonly`
  1. Navigate to the "Credentials"  page.
  1. Click "+ CREATE CREDENTIALS"
  1. Select "OAuth client ID" from the drop down.
  1. Set the "Application type" to "Desktop app" and make the name anything you'd like.
  1. Click "CREATE"
  1. This should give you a Client ID and Client Secret you can provide to `cmdg`.

```
$ cmdg -configure
[It will ask about ClientID and ClientSecret.
For now you have create one at https://console.developers.google.com]
Cut and paste this URL into your browser:
  https://long-url....
Returned code: <code shows up here, just FYI>
$
```
This creates `~/.cmdg/cmdg.conf`. To use another file, for example one per
account, pass `-config /path/to/file.conf` both here and when running `cmdg`.

### Updating an existing configuration
When a new version of `cmdg` needs a scope your sign-in did not grant, such
as `contacts.other.readonly` for suggesting people you have emailed, run
`cmdg -configure` again, with the same `-config` if you use one. It reuses
the ClientID and ClientSecret already in the file and only replaces the
sign-in, so the Cloud project needs no changes. If you are signed in to
several Google accounts, pick the one that file is for.

To check that it worked, run `cmdg -log /tmp/cmdg.log` (by default nothing is
logged) and, after up to five minutes, look for `Loaded correspondents`
without a `Failed to load Other contacts` line. The `Address scan:` lines in
between report the scan's progress: how many messages it has read, how many
failed, and the date of the oldest one read, which is how far back the
suggestions' dates reach.

## Running
```
$ cmdg
```
For keyboard shortcuts press '?' or F1 in most screens.

### Composing in a new window
**c** in the message list opens a new terminal window for the new message,
and the whole message is written there: the recipients, the editor,
attaching, and sending or saving it as a draft. The window closes when that
is done. The message list stays usable meanwhile, and each **c** opens
another window, so several messages can be written at once. Replies,
forwards and drafts continued with **C** are still written in the main
window.

The window runs `cmdg -compose`, a separate process with the same flags as
the one that opened it, in the same directory, so attachments are looked
for and failed sends saved there. It is given a copy of the address
suggestions as they were when **c** was pressed, in a temporary file it
deletes when it starts. It stays open after a sign-in or network error
until Enter is pressed, so that the error can be read, and if the terminal
never ran it at all, the message list shows what the terminal printed.

The terminal is opened with the `-terminal` flag, `st -e` by default: the
command line of the compose window is appended to it. For other terminals,
pass a command that runs what follows it, such as `-terminal="xterm -e"`.
With `-terminal=""`, or when neither `$DISPLAY` nor `$WAYLAND_DISPLAY` is
set, as over ssh, messages are composed in the main window as before.

With st under dwm, giving the window a class of its own lets a dwm rule
place it, for instance floating:

```
$ cmdg -terminal="st -c cmdg-compose -e"
```

```
/* config.h */
{ "cmdg-compose", NULL, NULL, 0, 1, -1 },
```

`cmdg -compose` can also be run on its own, for instance from a dwm key
binding, to write one message without the message list. Its suggestions
are then only your Google contacts, since the scan of sent and received
mail is done by the message list.

### Recipients
Composing or forwarding a message starts with three lines, To, CC and BCC,
with To selected. Each line takes a list of addresses separated by commas or
semicolons, and suggests contacts matching the address the cursor is in.

* **Tab** / **Shift-Tab**: Moves to the next or previous line.
* **Down** / **Up** (or **Ctrl-N** / **Ctrl-P**): Moves through the
  suggestions. The first is selected until you move.
* **Enter** with suggestions shown: Puts the selected one in the line in place
  of the address being typed. It is not suggested again until you change it.
* **Enter** with none shown: Sends all three lines to the editor. To send while
  suggestions are shown, press **Esc** twice to hide them, then **Enter**.
* **,** or **;**: Starts a new address. Inside quotes, as in `"Smith, John"
  <john@example.com>`, it is part of the name instead. An unquoted name with a
  comma is two addresses.
* **Left**, **Right**, **Home**, **End**, **Backspace**, **Delete**: Edit the
  line as text. **Ctrl-U** clears it. Moving the cursor into another address
  shows no suggestions for it until you change it.

**Esc** switches to vi's normal mode, shown by `-- NORMAL --`:

* **h**, **l**, **w**, **b**, **W**, **B**, **0**, **$**: Move the cursor. **W**
  and **B** move by whole address; **w** and **b** stop at `@` and `.`.
* **x** deletes the character under the cursor, **r** replaces it, **D**
  deletes to the end of the line and **C** changes to it.
* **d** and **c** followed by one of the motions above delete or change that
  far, and **dd** and **cc** the whole line.
* **i**, **a**, **I**, **A**: Return to typing, before or after the cursor, or
  at the start or end of the line.
* **j** / **k**: Move to the line below or above, stopping at BCC and To.
  **gg** and **G** go to To and BCC. **Tab** still cycles.
* **Enter**: Sends all three lines to the editor.

Normal mode does not search. If suggestions were shown when Esc was pressed,
they stay, and **j**, **k**, **gg**, **G**, **f**, **b**, **d** and **u** move
through them as in `less`, with **Enter** putting the selected one in the line.
**Esc** again, or changing the address, hides them.

A pasted list may be separated by commas, semicolons, tabs or newlines, and
becomes a comma-separated list. Suggestions are for its last address, so a list
pasted with a trailing separator shows none until more is typed. Empty
addresses, such as from a trailing comma, are dropped when the lines are sent
to the editor. `me` is replaced by your own address.

To may be left empty when CC or BCC is not. The message is then sent with
no To header, and the CC addresses are visible to everyone who gets it, as
usual, and the BCC addresses only to you.

To quit, press 'q'.
