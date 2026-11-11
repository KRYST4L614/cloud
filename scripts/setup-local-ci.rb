# Runs inside the local GitLab container through gitlab-rails runner.
require 'json'
require 'net/http'

root = User.find_by_username!('root')
token = root.personal_access_tokens.create!(
  name: 'cloud-ci-bootstrap', scopes: %w[api create_runner write_repository], expires_at: Date.tomorrow
)

begin
  api = lambda do |method, path, data = nil|
    request_class = Net::HTTP.const_get(method.capitalize)
    request = request_class.new('/api/v4' + path)
    request['PRIVATE-TOKEN'] = token.token
    request['Content-Type'] = 'application/json'
    request.body = JSON.generate(data) if data
    http = Net::HTTP.new('127.0.0.1', 9080, nil)
    http.read_timeout = 60
    response = http.request(request)
    raise "GitLab API #{method} #{path}: HTTP #{response.code}" unless response.is_a?(Net::HTTPSuccess)

    response.body.to_s.empty? ? nil : JSON.parse(response.body)
  end

  project = api.call('get', '/projects?search=cloud&owned=true').find { |p| p['path_with_namespace'] == 'root/cloud' }
  project ||= api.call('post', '/projects', {
    name: 'cloud', path: 'cloud', visibility: 'private', initialize_with_readme: false,
    shared_runners_enabled: false
  })
  id = project.fetch('id')
  api.call('put', "/projects/#{id}", { ci_forward_deployment_enabled: true })

  unless api.call('get', "/projects/#{id}/protected_tags").any? { |tag| tag['name'] == '*' }
    api.call('post', "/projects/#{id}/protected_tags", { name: '*', create_access_level: 40 })
  end

  variables = api.call('get', "/projects/#{id}/variables").map { |v| v['key'] }
  { 'DEPLOY_HOST' => 'VPS_IP', 'DEPLOY_USER' => 'VPS_USER' }.each do |key, value|
    next if variables.include?(key)

    api.call('post', "/projects/#{id}/variables", { key: key, value: value, protected: true })
  end

  runner = api.call('get', "/projects/#{id}/runners").find { |r| r['description'] == 'local-cloud-docker' }
  if runner
    runner_token = Ci::Runner.find(runner.fetch('id')).token
  else
    runner = api.call('post', '/user/runners', {
      runner_type: 'project_type', project_id: id, description: 'local-cloud-docker',
      tag_list: 'cloud-docker', run_untagged: false, locked: true, access_level: 'ref_protected'
    })
    runner_token = runner.fetch('token')
  end

  # stdout is redirected to a temporary file with mode 600, never printed.
  puts "CLOUD_PAT=#{token.token}"
  puts "CLOUD_RUNNER_TOKEN=#{runner_token}"
  puts "CLOUD_PROJECT_ID=#{id}"
rescue StandardError
  token.revoke!
  raise
end
