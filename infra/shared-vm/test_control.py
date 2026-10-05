import importlib.util
import unittest
from pathlib import Path
from unittest.mock import patch

spec = importlib.util.spec_from_file_location('control', Path(__file__).with_name('control.py'))
control = importlib.util.module_from_spec(spec)
spec.loader.exec_module(control)

class BrokerTests(unittest.TestCase):
    def setUp(self):
        control.TOKEN_CACHE.clear()

    def test_tokens_are_cached_by_identity_and_expire(self):
        with patch.object(control, 'iam', side_effect=[{'accessToken': 'a'}, {'accessToken': 'b'}, {'accessToken': 'new'}]) as mint:
            self.assertEqual(control.access_token('app-a')['access_token'], 'a')
            self.assertEqual(control.access_token('app-a')['access_token'], 'a')
            self.assertEqual(control.access_token('app-b')['access_token'], 'b')
            control.TOKEN_CACHE['app-a']['until'] = 0
            self.assertEqual(control.access_token('app-a')['access_token'], 'new')
            self.assertEqual(mint.call_count, 3)

    def handler(self, address, path, headers):
        handler = object.__new__(control.Handler)
        handler.client_address = (address, 1234)
        handler.path = path
        handler.headers = headers
        handler.server = type('Server', (), {'server_port': 8088})()
        handler.answer = lambda status, value, json_value=False: (status, value)
        return handler

    def test_unknown_network_and_missing_header_cannot_obtain_credentials(self):
        for address, headers in [('127.0.0.1', {'Metadata-Flavor': 'Google'}), ('172.28.1.2', {})]:
            handler = self.handler(address, '/computeMetadata/v1/instance/service-accounts/default/token', headers)
            with patch.object(control, 'iam') as mint:
                self.assertEqual(handler.do_GET()[0], 403)
                mint.assert_not_called()

    def test_network_selects_identity_and_cannot_select_another_account(self):
        config = '{"identities":{"gemihub":{"account":"gemi@example.com","project":"gemi"}}}'
        with patch.object(Path, 'read_text', return_value=config):
            handler = self.handler('172.28.1.2', '/computeMetadata/v1/instance/service-accounts/default/email', {'Metadata-Flavor': 'Google'})
            self.assertEqual(handler.do_GET(), (200, 'gemi@example.com'))
            handler.path = '/computeMetadata/v1/instance/service-accounts/other@example.com/token'
            self.assertEqual(handler.do_GET()[0], 404)

if __name__ == '__main__':
    unittest.main()
