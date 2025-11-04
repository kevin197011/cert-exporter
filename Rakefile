# frozen_string_literal: true

#
# This software is released under the MIT License.
# https://opensource.org/licenses/MIT

require 'time'

task default: %w[push]

task :push do
  system 'rubocop -A'
  system 'git add .'
  system "git commit -m \"Update #{Time.now}\""
  system 'git pull'
  system 'git push origin main'
end

task :run do
  system 'docker compose down'
  system 'docker compose up -d --build'
  system 'docker compose logs -f'
end
