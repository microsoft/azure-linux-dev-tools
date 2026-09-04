Name:    macro-continuation
Version: 1.0
Release: 1
Summary: %%define / %%global with backslash continuation
License: MIT

%global cmake_flags \
    -DENABLE_FOO=ON \
    -DENABLE_BAR=OFF \
    -DCMAKE_BUILD_TYPE=Release

%define configure_args \
    --prefix=%{_prefix} \
    --libdir=%{_libdir} \
    --sysconfdir=%{_sysconfdir}

%description
Fixture: %%define / %%global with backslash continuation lines.

%build
cmake %{cmake_flags} .
./configure %{configure_args}
make

%install
make install DESTDIR=%{buildroot}

%files
/usr/bin/macro-continuation

%changelog
* Thu Jan 01 1970 Builder <builder@example.com> - 1.0-1
- Initial fixture.
