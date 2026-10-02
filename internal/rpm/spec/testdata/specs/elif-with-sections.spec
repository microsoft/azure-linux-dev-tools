Name:    elif-with-sections
Version: 1.0
Release: 1
Summary: %%elif branches that each contain entire %%package sections
License: MIT

%description
Fixture: %%elif chain where every branch (including %%else) introduces a
distinct %%package + %%description + %%files trio. Each conditional branch
acts as a wrapper, not as in-section content.

%if 0%{?rhel}
%package rhel-extras
Summary: RHEL-specific extras

%description rhel-extras
Extras only built for RHEL.

%files rhel-extras
/usr/share/elif-with-sections/rhel
%elif 0%{?fedora}
%package fedora-extras
Summary: Fedora-specific extras

%description fedora-extras
Extras only built for Fedora.

%files fedora-extras
/usr/share/elif-with-sections/fedora
%else
%package generic-extras
Summary: Generic extras

%description generic-extras
Fallback extras for all other distros.

%files generic-extras
/usr/share/elif-with-sections/generic
%endif

%build
make

%install
make install DESTDIR=%{buildroot}

%files
/usr/bin/elif-with-sections

%changelog
* Thu Jan 01 1970 Builder <builder@example.com> - 1.0-1
- Initial fixture.
