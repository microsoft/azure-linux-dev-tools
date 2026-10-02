Name:    comment-only-conditional
Version: 1.0
Release: 1
Summary: %%if blocks whose entire body is comments and blank lines
License: MIT

%if 0%{?with_future}
# Reserved for the upcoming foo backend.
# Empty until upstream finalizes the API.

# Track: https://example.invalid/issues/42
%endif

%description
Fixture: a top-level conditional whose body contains only RPM-spec comments
and blank lines, plus a guard inside a script section with the same shape.

%build
%if 0%{?with_future}
# TODO(future): wire up the foo backend once it lands upstream.
%endif
make

%install
make install DESTDIR=%{buildroot}

%files
/usr/bin/comment-only-conditional

%changelog
* Thu Jan 01 1970 Builder <builder@example.com> - 1.0-1
- Initial fixture.
