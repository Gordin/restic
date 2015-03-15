# Maintainer: Andreas Guth <andreas.guth @t rwth-aachen.de>
# Contributer: Eldar Tsraev <elts@culab.org>

pkgname=restic-git
_pkgname=restic
pkgver=2014.47.278.g3d768e3
pkgrel=1
pkgdesc='A program that does backups right'
arch=('i686' 'x86_64')
url='https://github.com/restic/restic'
license=('BSD')
depends=('glibc')
makedepends=('git' 'go')
provides=('restic')
conflicts=('restic')
source=('git://github.com/restic/restic.git')
sha1sums=('SKIP')

_gitname='restic'

pkgver() {
  cd "$srcdir/$_gitname"
  git describe --tags | sed 's/-/./g'
}

build() {
  mkdir -p $srcdir/go
  export GOPATH=$srcdir/go
  rm -rf $GOPATH/src/
  go get -v github.com/${_pkgname}/${_pkgname}/cmd/${_pkgname}
}

package() {
  install -Dm644 $srcdir/go/src/github.com/${_pkgname}/${_pkgname}/LICENSE \
    $pkgdir/usr/share/licenses/$pkgname/LICENSE

  install -Dm755 $srcdir/go/bin/${_pkgname} \
    ${pkgdir}/usr/bin/${_pkgname}
}

# vim:set ts=2 sw=2 et:
