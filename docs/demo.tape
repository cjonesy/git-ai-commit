# Renders docs/demo.gif. Run it with `just demo`, which builds this checkout,
# prepares a scratch repo with a staged change in $DEMO_DIR, and uses your own
# ai-commit config for the model.

Output docs/demo.gif

Set Shell "bash"
Set FontSize 16
Set Width 1200
Set Height 640
Set Padding 20
Set TypingSpeed 40ms

Hide
# exit (ending the recording) rather than run anything outside the scratch repo
Type `cd "$DEMO_DIR/repo" || exit; export PATH="$DEMO_DIR/bin:$PATH" GIT_EDITOR="vim -u NONE --cmd 'set shortmess+=F'" GIT_PAGER=cat; clear`
Enter
Show

Type "git diff --cached --stat"
Enter
Sleep 2s

Type `git ai-commit -m "requests hung forever when the weather API was down"`
Enter
Sleep 1.5s
# skip the wait for the model; resume once the message is in the editor
Hide
Wait+Screen@120s /Please enter the commit message/
Show
Sleep 12s

Type ":wq"
Enter
Sleep 4s
