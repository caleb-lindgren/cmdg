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

The message list appears once the labels are loaded; your signature, settings
and contacts load meanwhile, and writing a message waits for them. A request
that only reads, or fetches an OAuth token, and gets no response within 10
seconds is made again on a new connection, after 20 and then 40 seconds more,
before it fails. Requests that change anything, such as sending, are never
made again, as one that got no response may still have been done. Requests
still waiting after 5 seconds are logged to the `-log` file.

### Searching
**/** in the message list asks for a query and sends it to Gmail, so
Gmail's search syntax applies: `in:trash`, `from:someone@example.com`,
`has:attachment`, and so on. In the results, **/** asks instead for a regular
expression ([Go syntax](https://pkg.go.dev/regexp/syntax), case insensitive)
and lists only the results whose From header, subject or snippet matches it.
Something that is not a valid expression, such as `a(`, is matched as it is
written. **/** again narrows the list further, and **/** with nothing
entered takes off the last filter. To search Gmail anew, go to the inbox
with **1** and press **/** there.

A filter applies to the results the list loads, not to every message the
query matches: a regular expression cannot be sent to Gmail, so only what has
been loaded can be matched against it. The list loads the first page Gmail
returns, of 100 messages, and further pages only while the terminal has rows
left to fill.

### Windows of their own
Opening a message (**Enter** in the message list), composing a new one
(**c**), replying (**r**), replying to all (**a**), forwarding (**f**) and
continuing a draft (**C**) each open a new terminal window. A message is
read there, and a message written there in full: choosing the draft, the
recipients, the editor, attaching, and sending or saving it as a draft. The
window closes when that is done. The message list stays usable meanwhile,
and each of those keys opens another window, so several messages can be
open and written at once. Replying from a message window opens yet another
window, so the message stays readable while you write.

In a message window, **^N** and **^P** move to the next and previous
message of the list as it was when the window was opened. **u**, **q**,
archiving, deleting and marking unread close the window. The message list
shows the changes made in a message window as soon as they are made: the
message read, starred, labelled or unlabelled, and taken out of the list
when archived, deleted, or unlabelled from the label listed. The window
tells the list over a Unix socket in a temporary directory only you can
open, created when the first message window is opened and removed when
cmdg exits. Should that fail, the list sees the changes when it next asks
Gmail what has changed, every 10 seconds, except in search results, which
are not updated that way.

Each window runs a separate cmdg process, with the same flags as the one that
opened it and one of `-read=<message ID>`, `-compose`, `-reply=<message ID>`,
`-reply_all=<message ID>`, `-forward=<message ID>` or `-continue_draft`. It
runs in the same directory, so files are attached from and saved to there. It
fetches the message itself, and is given a copy of the address suggestions, and
for a message window the IDs of the listed messages, as they were when the key
was pressed, in a temporary file it deletes when it starts. It stays open after
a sign-in or network error until Enter is pressed, so that the error can be
read, and if the terminal never ran it at all, the message list shows what the
terminal printed.

The terminal is opened with the `-terminal` flag, `st -e` by default: the
command line of the window is appended to it. For other terminals,
pass a command that runs what follows it, such as `-terminal="xterm -e"`.
With `-terminal=""`, or when neither `$DISPLAY` nor `$WAYLAND_DISPLAY` is
set, as over ssh, all of these are done in the main window as before.

With st under dwm, giving the windows a class of their own lets a dwm rule
place them, for instance floating:

```
$ cmdg -terminal="st -c cmdg-window -e"
```

```
/* config.h */
{ "cmdg-window", NULL, NULL, 0, 1, -1 },
```

`cmdg -compose` and `cmdg -continue_draft` can also be run on their own,
for instance from a dwm key binding, to write one message without the
message list. Suggestions are then only your Google contacts, since the
scan of sent and received mail is done by the message list.

### Conversations
With **T** in the message list, or `-conversations` to start that way, the
list shows one row per conversation rather than one per message: the newest
listed message of each thread, with the number of messages the conversation
shows when opened after the sender, as `Alice (3)`. That number is fetched
for each row as it is drawn, and until then is the number of messages listed
in the row. A row is bold if any of its listed messages is unread. Marking a
row and archiving, deleting, labelling or marking it read or unread acts on
every listed message in it. **T** again goes back to one row per message,
reloading the list.

Opening a row shows the whole conversation, oldest message first, starting at
the first unread one, including messages not in the label listed, such as your
replies, but not those in the trash or spam unless the opened one is. One
message is selected, marked by a ▶ before its sender line; **j** and **k**
select the next and previous message, **J** and **K** scroll by a line, and
scrolling a message off the screen selects the one at the edge. Text quoted
with `> `, with the "On ... wrote:" line before it, is folded into one line
saying how many lines it hides, as is the rest of a message from where the
message it replies to starts in Outlook's style: an "Original Message" line, or
a From, Sent (or Date) and To (or Subject) header block, with any line of
underscores above it, or an "On ... wrote:" line followed by text not quoted
with `> `, as Apple Mail sends it. The lines listing the attachments at the end
of a message are never folded. A header block with nothing above it, or under a
"Forwarded message" line, is a message forwarded, and is not folded. **z**
shows and hides the folded text of the selected message. Replying, forwarding,
the attachments (**t**) and the raw source (`\`) are those of the selected
message. Archiving, deleting and labelling act on the whole conversation,
marking unread on its newest message, and starring stars the newest message or,
if any is starred, unstars them all.

An open conversation checks its thread for new messages every ten seconds, as
the message list checks its label, and shows any that arrive at the end. A
message received that way, rather than one you sent or a draft, is told of on
the bottom line, highlighted, until it has been read: every line of it on
screen at some point, and the whole of it on screen (or, if it is taller than
the screen, the screen full of it) for ten seconds in all, with the window
having the focus. Only then is it marked read. If the window does not have the
focus when the message arrives, the terminal's bell is rung, which st turns
into an urgency hint and dwm into a highlighted tag until the window is
focused. Whether the window has the focus is known from the terminal's focus
reports, which st and xterm send; a terminal that sends none is taken to be
focused throughout, so it is never rung. The message list rings no bell.

In the message list, a message arriving moves its conversation's row to the
top, and the cursor stays on the row it was on.

The list is still built from Gmail's message list, grouped as it arrives, so
a conversation's row holds only the messages loaded so far: older messages of
a long-running thread on a page not yet loaded are not archived along with
the row, though opening it shows them.

### Replying
Replying (**r**) goes to the sender of the message, or its Reply-To, and
replying to all (**a**) also to its To and CC, leaving you out. Replying to a
message you sent instead goes to the people you sent it to: its To, and with
**a** its CC as well, as Gmail does, so a follow-up to your own last message
is not addressed to yourself. A message counts as sent by you when Gmail has
given it the SENT label. Its BCC is not carried over.

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
