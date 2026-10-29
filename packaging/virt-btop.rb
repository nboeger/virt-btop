# Homebrew formula, published to https://github.com/nboeger/homebrew-tap.
# The release workflow (bump-homebrew-tap.yml) copies this file there with the
# tag and sha256 filled in; keep it in sync with the tap.
# Users run: brew install nboeger/tap/virt-btop
class VirtBtop < Formula
  desc "btop-style TUI for libvirt/KVM virtual machines"
  homepage "https://github.com/nboeger/virt-btop"
  url "https://github.com/nboeger/virt-btop/archive/refs/tags/v1.2.0.tar.gz"
  sha256 "51dd2249405caec995b70491f7175a0f13cbf6600dd593742a990d7e4033b86d"
  license "GPL-3.0-only"

  depends_on "go" => :build
  depends_on "libvirt"

  def install
    system "go", "build", "-tags", "libvirt_dlopen",
           *std_go_args(ldflags: "-s -w -X main.version=#{version}", output: bin/"virt-btop"),
           "./cmd/virt-btop"
  end

  test do
    assert_match version.to_s, shell_output("#{bin}/virt-btop --version")
  end
end
