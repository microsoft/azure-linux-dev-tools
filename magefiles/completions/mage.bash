# --- MAGE COMPLETIONS START ---
# This section was created by the azldev tools' build system.
# Regenerate it by running 'mage completions'

_mage_completions()
{
    # Grab the contents of $(mage -l) once and store it.
    # Technically we should consider the case where mage is being called from a different directory via the -d flag,
    # but that adds complexity for a less common use case.
    # Disable colour output so there are no escape sequences in the output.
    # Enable hashfast so mage won't call out to 'go build' to check if the magefile has changed (this might cause
    # completions to be stale if the magefile has changed since mage was last run, but the performance gain is huge).
    mage_output=$( MAGEFILE_ENABLE_COLOR=false MAGEFILE_HASHFAST=true mage -l 2>/dev/null )
    mage_return_code=$?

    # If there is an error, don't attempt to complete (this should also cover the different directory case)
    if [ $mage_return_code -ne 0 ]; then
        return
    fi

    # Strip out undesired lines
    filtered_output=""
    while IFS= read -r line; do
        # 'mage -l' starts with "Targets:"
        [[ "$line" == *"Targets:"* ]] && continue
        # This is a special case for the 'azldev' tools, see mageutil.go:setCwdToProjectRoot()
        [[ "$line" == *"Current working directory:"* ]] && continue
        filtered_output+="$line
"
    done <<< "$mage_output"

    mage_output="$filtered_output"

    current_word="$2"
    previous_word="$3"

    # For the azldev tools, we want to also show some extra sub-targets for the 'check' and 'fix' commands.
    # Scrape the output to find those subcommands.
    #
    # i.e.
    # check     one of: [all, mod, lint, static, licenses].
    #
    # We would extract 'all, mod, lint, static, licenses'

    # Check if we have both a fix and check target available from mage.
    # If this is azldev tools we would like to show the sub-targets for check and fix.
    # See checkfix.go:Check(), Fix()
    check_line=$(echo "$mage_output" | grep -E '^[[:space:]]*check[[:space:]]*one of:')
    fix_line=$(echo "$mage_output" | grep -E '^[[:space:]]*fix[[:space:]]*one of:')

    # We expect both, otherwise just use the raw mage output
    if [ -n "$check_line" ] && [ -n "$fix_line" ]; then
        check_targets=$(echo "$check_line" | grep -o '\[.*\]' | tr -d '[],')
        fix_targets=$(echo "$fix_line" | grep -o '\[.*\]' | tr -d '[],')

        # If the last command is "check" then we want to only show the check targets which are 'all, mod, lint, static, licenses'
        if [ "${previous_word}" == "check" ]; then
            mapfile -t check_targets <<< "$(_mage_completions_case_insensitive_compgen "${check_targets}" "${current_word}" )"
            COMPREPLY+=( "${check_targets[@]}" )
            return
        fi

        # If the last command is "fix", we have 'all, mod, lint'
        if [ "${previous_word}" == "fix" ]; then
            mapfile -t fix_targets <<< "$(_mage_completions_case_insensitive_compgen "${fix_targets}" "${current_word}" )"
            COMPREPLY+=( "${fix_targets[@]}" )
            return
        fi
    fi

    words="$(cat  <<< "$mage_output" | awk '{print $1}')"
    mapfile -t all_targets <<< "$(_mage_completions_case_insensitive_compgen "${words}" "${current_word}" )"
    COMPREPLY+=( "${all_targets[@]}" )
}

# Rudimentary version of compgen that is case insensitive, but supports no flags.
# _mage_completions_case_insensitive_compgen "word1 word2 ..." "current_word"
_mage_completions_case_insensitive_compgen()
{
    local words="$1"
    local current_word="$2"

    #echo "completing for $current_word" 1>&2

    for word in $words; do
        if [[ "${word,,}" == "${current_word,,}"* ]]; then
            # Insert a space after the word so that the user can continue typing (to counteract the behaviour of
            # -o nospace)
            echo "$word "
        fi
    done
}

# Mage doesn't support arbitrary arguments, so we don't want to generate a space unless the user has typed a
# valid target.
complete -o nospace -F _mage_completions mage

# End of auto-generated completion for azldev tools.
# --- MAGE COMPLETIONS END ---
