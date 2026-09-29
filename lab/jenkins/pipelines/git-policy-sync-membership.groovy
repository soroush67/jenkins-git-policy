// Phase 10: sync GitLab group membership into the git-policy cache.
// Groups come from the ACTIVE policy (ctl groups); the API token stays in
// Jenkins; the server applies the file with its safety guard.
pipeline {
    agent any
    triggers { cron('H/15 * * * *') }
    options { timestamps(); disableConcurrentBuilds(); buildDiscarder(logRotator(numToKeepStr: '50')) }
    parameters {
        booleanParam(name: 'FORCE', defaultValue: false, description: 'Override the >30% drop / missing-group guard (check GitLab first!)')
    }
    stages {
        stage('sync membership') {
            steps {
                sshagent(credentials: ['git-policy-ssh-test']) {
                    withCredentials([string(credentialsId: 'gitlab-api-readonly', variable: 'GITLAB_TOKEN')]) {
                        sh '''
                            set -eu
                            SSH="ssh -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o BatchMode=yes gitpolicy-deploy@gp-ctl"
                            groups=$($SSH groups)
                            if [ -z "$groups" ]; then echo "active policy uses no groups: nothing to sync"; exit 0; fi
                            echo "groups used by the active policy: $(echo $groups)"
                            GP=$(ls /opt/git-policy/git-policy-*-linux-amd64 | tail -1)
                            "$GP" sync-membership --gitlab-url http://gitlab --groups "$groups" -o membership.json --inventory-out inventory.json
                            actor=$(printf '%s' "jenkins#${BUILD_NUMBER}" | base64 -w0)
                            force=""; if [ "${FORCE:-false}" = true ]; then force=" --force"; fi   # unset on the very first run of a new job
                            $SSH "apply-membership --actor-b64=$actor$force" < membership.json
                        '''
                    }
                }
            }
        }
    }
    post { always { cleanWs() } }
}
