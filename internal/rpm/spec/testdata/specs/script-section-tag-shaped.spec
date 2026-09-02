Name:    script-section-tag-shaped
Version: 1.0
Release: 1
Summary: Tag-shaped shell lines inside script sections must not be parsed as tags
License: MIT

%description
Fixture: script sections (%%build, %%install, %%post, %%pre, %%check) contain
shell commands whose arguments look exactly like spec tags
(`echo "Name: foo"`, `printf "Version: ...\n"`, etc.). Tag-edit operations
must skip these — only the preamble and %%package blocks accept tag writes.

%build
echo "Name: not-a-tag-write"
printf "Version: still-not-a-tag\n"
echo "Requires: bash" >> .build-manifest
make

%install
make install DESTDIR=%{buildroot}
cat <<EOF > %{buildroot}/etc/%{name}.conf
Name: %{name}
Version: %{version}
EOF

%check
echo "License: MIT" | tee -a check.log
make check

%pre
echo "Conflicts: previous-version" >&2

%post
ldconfig
echo "Provides: %{name}-runtime" > /var/log/%{name}-post.log

%files
/usr/bin/script-section-tag-shaped
/etc/%{name}.conf

%changelog
* Thu Jan 01 1970 Builder <builder@example.com> - 1.0-1
- Initial fixture.
