Name:    subpackage-define-shadowed
Version: 1.0
Release: 1
Summary: Subpackage %%define shadows a surviving preamble macro
License: MIT

%global toolsdir %{_libdir}/%{name}

%description
Fixture verifying that a subpackage %%define whose name already has a
surviving definition in the preamble is NOT hoisted. The survivor reference
in %%install resolves to the existing preamble definition, so hoisting the
subpackage copy would wrongly clobber it.

%package tools
Summary: Tools for %{name}

%global toolsdir %{_libdir}/%{name}/tools-override

%description tools
Tools for %{name}.

%files tools
%{toolsdir}

%install
mkdir -p %{buildroot}%{toolsdir}

%files
/usr/bin/subpackage-define-shadowed

%changelog
* Thu Jan 01 1970 Builder <builder@example.com> - 1.0-1
- Initial fixture.
