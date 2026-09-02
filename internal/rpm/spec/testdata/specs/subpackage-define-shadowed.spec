Name:    subpackage-define-shadowed
Version: 1.0
Release: 1
Summary: Subpackage %%define overrides a surviving preamble macro
License: MIT

%global toolsdir %{_libdir}/%{name}

%description
Fixture verifying that a subpackage %%define whose name already has a
surviving definition in the preamble is hoisted when it is the exact effective
binding of the surviving %%install reference.

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
