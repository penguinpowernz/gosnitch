# gosnitch

A lightweight [Fyne](https://fyne.io) UI for the [OpenSnitch](https://github.com/evilsocket/opensnitch)
application firewall, as a simpler alternative to the stock Python UI
(`opensnitch-ui`). It lives in the system tray and shows one plain table of
connection events.

## How it fits together

OpenSnitch inverts the usual client/server roles: **the UI is the gRPC server**
and `opensnitchd` connects *out* to it. gosnitch therefore listens on
`/tmp/osui.sock` (the `Server.Address` in `/etc/opensnitchd/default-config.json`)
and implements the `protocol.UI` service:

| RPC | What gosnitch does |
| --- | --- |
| `Subscribe` | Records that the daemon has attached, and its version. |
| `Ping` | Heartbeat, once a second. Drives the connected/disconnected status. |
| `AskRule` | A connection matched no rule. Prompts (or applies the default) and returns the verdict. |
| `Notifications` | Bidirectional stream. Held open, and used to push `DELETE_RULE` down to the daemon. |

The `.proto` in `proto/ui.proto` was reconstructed from the descriptor shipped
with `opensnitch-ui` 1.5.8, so it is wire-compatible with that daemon.

## Building

Fyne needs cgo and the usual X11/OpenGL headers:

```sh
sudo apt install golang gcc libgl1-mesa-dev xorg-dev
CGO_ENABLED=1 go build -o gosnitch ./cmd/gosnitch
```

## Running

Only one UI can own the socket, so stop the Python one first:

```sh
systemctl --user stop opensnitch-ui    # or just kill it
./gosnitch
```

The daemon reconnects on its own within a few seconds.

### Options

| Flag | Default | Meaning |
| --- | --- | --- |
| `-address` | `unix:///tmp/osui.sock` | Where to listen. Must match the daemon's `Server.Address`. |
| `-rules` | `/etc/opensnitchd/rules` | Directory the daemon keeps rules in. Read-only to gosnitch. |
| `-interactive` | `true` | Prompt on unmatched connections. `false` records silently and applies the default. |
| `-default-action` | `allow` | `allow`, `deny` or `reject` when a prompt goes unanswered. Only seeds the initial value; the tray changes it afterwards (allow and deny only). |
| `-hidden` | `false` | Start minimised to the tray. |

`GOSNITCH_ADDRESS` overrides the default address.

## Using it

Two tabs:

**Events** — live connections, newest first, capped at 1000 rows. **Clear**
empties it.

**Rules** — every rule in the rules directory, newest first by `created`.
Select one and press **Delete rule**; gosnitch confirms first, because
deletion cannot be undone.

- **Tray icon** → *Show events*, *Manage rules*, *Default action*, *Quit*.
- **Default action** picks what an unanswered prompt does: *Allow* or *Deny*.
  The choice is remembered across restarts, and the countdown moves to that
  button. `reject` is accepted from `-default-action` but is not offered in
  the menu, which keeps it a quick switch rather than something to read.
- Closing the window **hides** it to the tray rather than quitting.

### The prompt

The prompt is built around one idea: a security prompt you answer many times a
day must be answerable **without careful aiming**. Small checkboxes add friction,
and friction on a security tool trains you to click through it.

So every control is a full-size button:

```
  firefox wants to connect to www.mozilla.org

  For how long
  [ Once ][ 30 sec ][ 5 min ][ 1 hour ][ Until reboot ][ Forever ]

  Apply to
  Always limited to firefox. Narrow it further:
  [ ✓ www.mozilla.org ][   port 443 ][   user 1000 ]

  [   Deny (58)    ][      Allow      ]
```

- **Duration** is a single-select row: clicking one deselects the rest.
- **Scope** toggles are independent, and each shows the value it pins to, so
  you can see what you are enabling. The rule is always limited to the
  executable; these narrow it further.
- The two shapes this is tuned for are *deny forever to a destination* and
  *allow forever, pinned to destination + port + user*.

**Enter does nothing.** A prompt can appear at any moment, including
mid-keystroke while you are typing somewhere else, and an Enter landing on
*Allow* would create a rule you never saw. Return, Enter and Space are all
inert, and the window focuses no widget, so only a deliberate click answers.
Escape dismisses the prompt, which applies the default action rather than
creating a rule.

The countdown rides on the button the timeout would press - `Deny (58)` - so
the default is visible exactly where it will land, and the other button stays
plain. Change which one that is from the tray.

**Touching any button cancels the timeout.** Picking a duration or flipping a
scope toggle proves you are there and deciding, so the counter disappears and
the prompt waits for a deliberate Allow or Deny - however long you take. The
timeout exists for prompts nobody is looking at, not to race someone who is.

Two things are deliberately **not** configurable:

- The prompt **timeout is fixed at 60 seconds**, and only applies to an
  untouched prompt. It is the window in which an unattended machine decides
  for itself, so it should not drift.
- An unanswered prompt always applies the default action **for `once` only**,
  whatever the duration buttons show. A timeout can never create a lasting
  rule.

### How deleting works

gosnitch never touches the rule files: they are owned by root, and the daemon
is their authority. Deleting sends a `DELETE_RULE` notification naming the
rule, exactly as the Python UI does, and `opensnitchd` removes the file. So:

- Deleting needs the daemon **connected**; gosnitch refuses otherwise.
- gosnitch reads `/etc/opensnitchd/rules` directly to list rules, because only
  the files carry the `created` timestamps the newest-first ordering needs.
- Both `simple` and `list` rules are understood; a `list` rule is flattened so
  its process, destination, port and user show in their own columns.

Rules are named with the same slug scheme the Python UI uses
(`allow-once-simple-usr-bin-curl`), so both clients produce consistent entries
in `/etc/opensnitchd/rules`.

## Development

`cmd/rulesdump` prints the parsed rules in the order the UI shows them, which
is the quickest way to check parsing against a real rules directory:

```sh
go run ./cmd/rulesdump -n 20
```

`cmd/mockdaemon` impersonates `opensnitchd` so the UI can be driven without
root:

```sh
CGO_ENABLED=1 go build -o gosnitch ./cmd/gosnitch
go build -o mockdaemon ./cmd/mockdaemon

./gosnitch -address unix:///tmp/gosnitch-test.sock &
./mockdaemon -address unix:///tmp/gosnitch-test.sock -every 1s
```

Regenerating the protobuf bindings needs `protoc` plus `protoc-gen-go` and
`protoc-gen-go-grpc`:

```sh
protoc --go_out=. --go_opt=module=github.com/penguinpowernz/gosnitch \
       --go-grpc_out=. --go-grpc_opt=module=github.com/penguinpowernz/gosnitch \
       -I proto proto/ui.proto
```

## Caveats

- Only one OpenSnitch UI can bind the socket at a time.
- gosnitch can list and delete rules, but not edit or create them beyond the
  rule implied by answering a prompt. Use the Python UI to edit.
- Rules gosnitch writes are scoped to the executable, optionally narrowed by
  destination, port and user. The Python UI offers further operands (process
  command line, network ranges, regex matches) that gosnitch does not author,
  though it displays them correctly.
- No rule history: events are kept in memory only and lost on exit.
