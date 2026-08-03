import './styles.css';
import { installEndpointsModule } from './features/endpoints';
import { installProfilesModule } from './features/profiles';
import { installSubscriptionsModule } from './features/subscriptions';

installEndpointsModule(document.getElementById('endpointSettingsMount'));
installProfilesModule(document.getElementById('profileSettingsMount'));
installSubscriptionsModule(document.getElementById('subscriptionSourcesMount'));
