test "$(id -u)" = 0
test ! -e /workdir/project/azldev.toml
jq -ne '1 + 1 == 2'
echo root-helper-sourced
