Name:    if-with-continuation
Version: 1.0
Release: 1
Summary: %%if condition that spans multiple lines via backslash continuation
License: MIT

%global _is_long_arch \
    0%{?rhel} >= 9 || \
    0%{?fedora} >= 40 || \
    0%{?suse_version} >= 1550

%if %{_is_long_arch} && \
    %{undefined disable_long_arch} && \
    "%{_arch}" != "armv7hl"
BuildRequires: long-arch-support
Requires: long-arch-runtime
%endif

%description
Fixture: backslash-continuation inside an %%if condition itself (not just in
the body) and in a %%global that the condition references.

%build
make

%install
make install DESTDIR=%{buildroot}

%files
/usr/bin/if-with-continuation

%changelog
* Thu Jan 01 1970 Builder <builder@example.com> - 1.0-1
- Initial fixture.
