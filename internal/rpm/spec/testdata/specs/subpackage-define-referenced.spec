Name:    subpackage-define-referenced
Version: 1.0
Release: 1
Summary: %%define inside a subpackage referenced from %%install (issue #203 repro)
License: MIT

%description
Fixture mirroring issue #203 -- the helper macro is defined inside the
test subpackage but referenced from the unconditional install section.
Removing the subpackage naively drops the macro and leaves dangling
references in surviving sections.

%package tests
Summary: Tests for %{name}
Requires: %{name} = %{version}-%{release}

%define testsdir %{_libdir}/%{name}/tests-src

%description tests
The %{name}-tests rpm contains test fixtures for %{name}.

%files tests
%{testsdir}

%build
make

%install
make install DESTDIR=%{buildroot}
mkdir -p %{buildroot}%{testsdir}/python
mkdir -p %{buildroot}%{testsdir}/scripts
install -p -m 0644 tests/Makefile.include %{buildroot}%{testsdir}/

%files
/usr/bin/subpackage-define-referenced

%changelog
* Thu Jan 01 1970 Builder <builder@example.com> - 1.0-1
- Initial fixture.
