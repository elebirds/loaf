# Run the optional Bridge after login

Bind destinations and create subscriptions with the same user's current Loaf
build first. The Bridge also stays alive when there are no subscriptions yet.
These are opt-in user-service templates, not an installer; ordinary `loaf run`
does not need them. Sleep still stops local delivery, and remote disks must
survive for later collection.

## macOS launchd

Copy `com.elebirds.loaf.notify.plist` to
`~/Library/LaunchAgents/com.elebirds.loaf.notify.plist`. Replace the executable
and log placeholders with absolute paths; create a private log directory first.
Use the same XDG configuration/state bases as your interactive CLI. If you use
nondefault XDG bases, add those nonsecret EnvironmentVariables explicitly.

```sh
plutil -lint "$HOME/Library/LaunchAgents/com.elebirds.loaf.notify.plist"
launchctl bootstrap "gui/$(id -u)" "$HOME/Library/LaunchAgents/com.elebirds.loaf.notify.plist"
launchctl print "gui/$(id -u)/com.elebirds.loaf.notify"
# Stop and unload before updating the binary or starting a foreground Bridge:
launchctl bootout "gui/$(id -u)" "$HOME/Library/LaunchAgents/com.elebirds.loaf.notify.plist"
```

## Linux systemd user service

Copy `loaf-notify.service` to `~/.config/systemd/user/loaf-notify.service` and
replace its executable placeholder with the absolute built binary path.

```sh
systemctl --user daemon-reload
systemctl --user enable --now loaf-notify.service
systemctl --user status loaf-notify.service
journalctl --user -u loaf-notify.service
# Stop and disable:
systemctl --user disable --now loaf-notify.service
```

Startup is at user login. Boot without a login depends on the user's existing
service-manager configuration; these templates do not enable lingering or change
system policy. A desktop Agent must still be available for its destination.

Bearer credentials use the service's explicitly supplied environment. Do not
put token values in these templates, argv, shared project files or logs; arrange
secret injection through your existing service environment. A missing variable
fails before an HTTP send and does not fall back to another credential source.
Only one Bridge may own a state directory. Stop the service before manually
running `loaf notify serve` against that same state.
