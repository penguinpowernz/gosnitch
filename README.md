# gosnitch

A lightweight [Fyne](https://fyne.io) UI for the [OpenSnitch](https://github.com/evilsocket/opensnitch)
application firewall, as a simpler alternative to the stock Python UI
(`opensnitch-ui`). It lives in the system tray and shows one plain table of
connection events.

## Building

Fyne is a cgo library that binds OpenGL and X11 directly, so a plain `go build`
is not enough: it needs a C toolchain and the X11/OpenGL development headers
present at compile time. On a Debian-based system:

```sh
sudo apt install libgl1-mesa-dev xorg-dev libxcursor-dev libxrandr-dev libxinerama-dev libxi-dev libxxf86vm-dev
```

Then build with cgo enabled:

```sh
CGO_ENABLED=1 go build -o gosnitch ./cmd/gosnitch
```

The `Makefile` sets and exports `CGO_ENABLED=1` itself, so `make build` works
without the prefix.

## Packaging

The repo is a Debian package, managed with [go-ian](https://github.com/penguinpowernz/go-ian):

```sh
make deb        # builds usr/bin/gosnitch, then runs ian pkg
```

The `.deb` lands in `pkg/`. Install it with `sudo dpkg -i pkg/gosnitch_*.deb`.

`ian` packages the whole working tree minus `.ianignore`, so the install tree
lives in the repo as `usr/` and `etc/`: the binary at `/usr/bin/gosnitch`, a
menu entry, and an autostart entry that launches `gosnitch -hidden` at login.
Source directories are listed in `.ianignore` to keep them out of the package.

Bump the version with `ian set -v 1.2.3` before building; edit `DEBIAN/control`
directly for fields `ian set` does not cover.

The package `Conflicts` with `opensnitch-ui`, since only one UI can bind the
daemon socket.

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
| `-version` | | Print the version and exit. |

`GOSNITCH_ADDRESS` overrides the default address.

## Using it

The main window is **a lot** simpler than the Python frontend.

<img width="853" height="511" alt="image" src="https://github.com/user-attachments/assets/6640399f-dfa7-4cfd-aef3-202cf1198fdf" />

Two tabs:

**Events** — live connections, newest first, capped at 1000 rows. **Clear**
empties it.

**Rules** — every rule in the rules directory, newest first by `created`.
Select one and press **Delete rule**; gosnitch confirms first, because
deletion cannot be undone. A rule file that cannot be parsed is counted next
to the total (`267 rules — 1 rule unreadable`) and logged, since a rule the
daemon is still enforcing should not just be missing from the list.

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

<img width="634" height="548" alt="image" src="https://github.com/user-attachments/assets/fe07d1ef-5628-4c39-980c-f6f13701e687" />

- **Duration** is a single-select row: clicking one deselects the rest. Six
  buttons across set the window's width on their own, so they wrap to two rows
  of three.
- **Scope** toggles are independent, and each shows the value it pins to, so
  you can see what you are enabling. The rule is always limited to the
  executable; these narrow it further. A long hostname is elided in the middle
  on the button, which keeps both ends readable; the full value is in the
  detail rows above.
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

**One prompt at a time.** A burst of unmatched connections would otherwise
open a stacked window per connection, each with its own countdown, which is
not something anyone can answer. Prompts queue instead, and past 8 waiting the
rest are declined immediately so the daemon applies the default action -
shedding is the safer failure, since the default is what an unanswered prompt
applies anyway.

Every value shown in the prompt comes from the process being judged, so each
is sanitised before it reaches a label: control characters and bidi overrides
become U+FFFD. Without that a newline in a hostname could forge the lines
below it and attribute the connection to a different binary. It is
display-only, so the operands written into a rule are unchanged.

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
in `/etc/opensnitchd/rules`. The daemon keys rules by name, so a name has to
say which value it came from: slugifying leaves nothing behind for a value with
no ASCII alphanumerics (a CJK-named binary collapses to just its parent
directory), and only that case earns a short digest suffix. Ordinary paths,
hostnames and numbers keep the byte-for-byte name the Python UI writes, so
equivalent rules from either client still collide on purpose.

## The OpenSnitch protocol

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

### The socket

`/tmp/osui.sock` is in a world-writable directory, and every process running as
this user can reach it - not only `opensnitchd`. `$XDG_RUNTIME_DIR` would be a
better home, but the daemon ships `unix:///tmp/osui.sock` in its
`default-config.json`, so moving it would break the connection unless the
daemon's config were edited too. gosnitch hardens the path it has to use
instead:

- The socket is bound under a `0o007` umask, so it is never created wider than
  `0770`. Binding and then narrowing it with `chmod` leaves a window in which
  anything can connect, and that window is reachable in practice. The mode is
  checked after the bind rather than assumed.
- A stale socket is only removed when we own it. `lstat`, so a planted symlink
  is not followed, and a non-socket at that path is refused rather than
  deleted.
- The gRPC server caps concurrent streams, sets a connection timeout and a
  keepalive policy, all well clear of what a real daemon needs (one
  `Notifications` stream plus occasional unary calls). `MaxRecvMsgSize` stays
  at 32MB because `Statistics` carries unbounded maps.
- Events are held in a ring buffer, so recording one is constant-time. It used
  to prepend to a slice, which copied the whole buffer per event - a cost any
  local process could drive by opening connections.


## Development

`cmd/rulesdump` prints the parsed rules in the order the UI shows them, which
is the quickest way to check parsing against a real rules directory:

```sh
go run ./cmd/rulesdump -n 20
```

`cmd/mockdaemon` impersonates `opensnitchd` so the UI can be driven without
root. `make run-dev` builds both and points them at a throwaway socket, or by
hand:

```sh
CGO_ENABLED=1 go build -o gosnitch ./cmd/gosnitch
go build -o mockdaemon ./cmd/mockdaemon

./gosnitch -address unix:///tmp/gosnitch-test.sock &
./mockdaemon -address unix:///tmp/gosnitch-test.sock -every 1s
```

```sh
make test    # go test -race ./...
make vet
```

Much of what the tests cover is behaviour that fails silently if it regresses,
so they are worth reading before changing that code: that an idle
`Notifications` stream survives the connection-idle timeout, that the socket is
never created world-reachable, that ring-buffer ordering holds when wrapped,
that the prompt's content fits inside its window, and that rule names still
match a real `/etc/opensnitchd/rules`.

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
