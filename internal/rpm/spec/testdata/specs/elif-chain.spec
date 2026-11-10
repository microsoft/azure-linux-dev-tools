Name:    elif-chain
Version: 1.0
Release: 1
Summary: %%if / %%elif / %%else chain inside preamble
License: MIT

%if 0%{?rhel} >= 10
Requires: rhel10-runtime
BuildRequires: rhel10-devel
%elif 0%{?rhel} >= 9
Requires: rhel9-runtime
BuildRequires: rhel9-devel
%elif 0%{?fedora} >= 40
Requires: fedora-runtime
BuildRequires: fedora-devel
%elif 0%{?suse_version}
Requires: suse-runtime
BuildRequires: suse-devel
%else
Requires: generic-runtime
BuildRequires: generic-devel
%endif

%description
Fixture: deep %%elif chain with terminal %%else, content-style conditional
(no section headers in any branch).

%build
make

%install
make install DESTDIR=%{buildroot}

%files
/usr/bin/elif-chain

%changelog
* Thu Jan 01 1970 Builder <builder@example.com> - 1.0-1
- Initial fixture.
