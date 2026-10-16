import './styles.css';
import { installShell } from './features/shell';
import { installEndpointsModule } from './features/endpoints';
import { installProfilesModule } from './features/profiles';
import { installSubscriptionsModule } from './features/subscriptions';
import { installJobsModule } from './features/jobs';

installEndpointsModule(document.getElementById('endpointSettingsMount'));
installProfilesModule(document.getElementById('profileSettingsMount'));
installSubscriptionsModule(document.getElementById('subscriptionSourcesMount'));
installJobsModule(document.getElementById('jobSettingsMount'), document.getElementById('jobRuntimeMount'));

installShell();
