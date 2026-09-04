Name:    straddling-wrapper
Version: 1.0
Release: 1
Summary: %%if opens before a section header and %%endif closes inside it
License: MIT

%description
Fixture: classic Fedora-style "straddling" conditional. The %%if directive
appears at the top level (between %%build and %%install) but is paired with
an %%endif that lives several sections later — bracketing %%install and
%%check into the conditional wrapper.

%build
make

%if 0%{?with_tests}
%install
make install DESTDIR=%{buildroot}
make install-tests DESTDIR=%{buildroot}

%check
make check
%endif

%files
/usr/bin/straddling-wrapper

%changelog
* Thu Jan 01 1970 Builder <builder@example.com> - 1.0-1
- Initial fixture.
