# typed: false
# frozen_string_literal: true

class LetGo < Formula
  desc "A Clojure dialect implemented as a bytecode VM in Go"
  homepage "https://github.com/nooga/let-go"
  license "MIT"
  version "2.0.1"

  on_macos do
    on_intel do
      url "https://github.com/nooga/let-go/releases/download/v2.0.1/let-go_2.0.1_darwin_amd64.tar.gz"
      sha256 "cb80ba513685e801b7710ae9175bc936f75b0b0714b6bf4ea3087ce1911384e2"
    end
    on_arm do
      url "https://github.com/nooga/let-go/releases/download/v2.0.1/let-go_2.0.1_darwin_arm64.tar.gz"
      sha256 "d4a5c141f9615a9b57c0554906e1baa3734dbef28529e79663e642ec8fc11e8e"
    end
  end

  on_linux do
    on_intel do
      url "https://github.com/nooga/let-go/releases/download/v2.0.1/let-go_2.0.1_linux_amd64.tar.gz"
      sha256 "d126c660f4a1e875443704c3ef01b6b1a7fd4d5e3def093f2c248073e0c501fe"
    end
    on_arm do
      url "https://github.com/nooga/let-go/releases/download/v2.0.1/let-go_2.0.1_linux_arm64.tar.gz"
      sha256 "6844b7cb195301fd2c5de1239be555a5c3e16efcf5cc531b13b268f4953cbb33"
    end
  end

  def install
    bin.install "lg"
  end

  test do
    assert_equal "2", shell_output("#{bin}/lg -e '(+ 1 1)'").strip
  end
end
