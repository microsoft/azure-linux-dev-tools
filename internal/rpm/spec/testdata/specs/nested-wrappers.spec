Name:    nested-wrappers
Version: 1.0
Release: 1
Summary: %%if wrappers nested inside other %%if wrappers across sections
License: MIT

%description
Fixture: outer %%if wraps the %%package devel section, which itself contains
an inner %%if that wraps %%description devel / %%files devel.

%if 0%{?with_devel}
%package devel
Summary: Development files
Requires: %{name} = %{version}-%{release}

%if 0%{?with_devel_docs}
%description devel
Devel files for nested-wrappers, including extra documentation.

%files devel
/usr/include/nested-wrappers.h
/usr/share/doc/nested-wrappers/devel/
%else
%description devel
Devel files for nested-wrappers.

%files devel
/usr/include/nested-wrappers.h
%endif
%endif

%build
make

%install
make install DESTDIR=%{buildroot}

%files
/usr/bin/nested-wrappers

%changelog
* Thu Jan 01 1970 Builder <builder@example.com> - 1.0-1
- Initial fixture.
