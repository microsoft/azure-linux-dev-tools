Name:    subpackage-define-transitive
Version: 1.0
Release: 1
Summary: Transitive %%define chain inside a subpackage (issue #203 follow-up)
License: MIT

%description
Fixture for the transitive macro-hoisting case: the subpackage defines a
chain of helper macros (%%testroot -> %%testsdir) and only the outer one is
referenced from the surviving %%install section. Removing the subpackage must
hoist BOTH macros so the survivor reference resolves.

%package tests
Summary: Tests for %{name}
Requires: %{name} = %{version}-%{release}

%define testroot %{_libdir}/%{name}
%define testsdir %{testroot}/tests-src

%description tests
The %{name}-tests rpm contains test fixtures for %{name}.

%files tests
%{testsdir}

%build
make

%install
make install DESTDIR=%{buildroot}
mkdir -p %{buildroot}%{testsdir}/python

%files
/usr/bin/subpackage-define-transitive

%changelog
* Thu Jan 01 1970 Builder <builder@example.com> - 1.0-1
- Initial fixture.
