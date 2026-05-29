Name:    subpackage-define-unreferenced
Version: 1.0
Release: 1
Summary: %%define inside a subpackage only referenced from within itself
License: MIT

%description
Fixture companion to subpackage-define-referenced. The macro defined inside
the helper subpackage is only referenced from within the same subpackage,
so removing that subpackage should drop the macro cleanly without any
hoisting.

%package tools
Summary: Helper tools for %{name}
Requires: %{name} = %{version}-%{release}

%define toolsdir %{_libexecdir}/%{name}/tools

%description tools
Helper command-line utilities used only with the tools subpackage.

%files tools
%{toolsdir}

%build
make

%install
make install DESTDIR=%{buildroot}

%files
/usr/bin/subpackage-define-unreferenced

%changelog
* Thu Jan 01 1970 Builder <builder@example.com> - 1.0-1
- Initial fixture.
