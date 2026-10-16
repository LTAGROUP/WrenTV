import Foundation
import Security

enum Keychain {
    private static let service = "com.wrentv.mac"
    /// Items saved before the rename. They're copied on first read and
    /// removed on sign-out, so existing sign-ins carry over.
    private static let legacyService = "com.haven.mac"

    static func get(_ account: String) -> String? {
        if let v = read(account, service: service) { return v }
        guard let v = read(account, service: legacyService) else { return nil }
        set(v, for: account)
        return v
    }

    private static func read(_ account: String, service: String) -> String? {
        let q: [String: Any] = [kSecClass as String: kSecClassGenericPassword, kSecAttrService as String: service,
                                kSecAttrAccount as String: account, kSecReturnData as String: true]
        var out: AnyObject?
        guard SecItemCopyMatching(q as CFDictionary, &out) == errSecSuccess, let d = out as? Data else { return nil }
        return String(data: d, encoding: .utf8)
    }

    static func set(_ value: String?, for account: String) {
        let q: [String: Any] = [kSecClass as String: kSecClassGenericPassword, kSecAttrService as String: service,
                                kSecAttrAccount as String: account]
        SecItemDelete(q as CFDictionary)
        if value == nil {
            let legacy: [String: Any] = [kSecClass as String: kSecClassGenericPassword,
                                         kSecAttrService as String: legacyService, kSecAttrAccount as String: account]
            SecItemDelete(legacy as CFDictionary)
        }
        guard let value else { return }
        var add = q
        add[kSecValueData as String] = Data(value.utf8)
        SecItemAdd(add as CFDictionary, nil)
    }
}
