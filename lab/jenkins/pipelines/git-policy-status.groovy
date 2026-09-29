// Lab smoke job: git-policy status through the git-policy-ctl forced-command channel.
pipeline {
    agent any
    options { timestamps(); disableConcurrentBuilds() }
    stages {
        stage('git-policy status') {
            steps {
                sshagent(credentials: ['git-policy-ssh-test']) {
                    // status exits 1 on WARNING; only CRITICAL (3) fails the build
                    sh 'ssh -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o BatchMode=yes gitpolicy-deploy@gp-ctl status || test $? -eq 1'
                }
            }
        }
    }
}
