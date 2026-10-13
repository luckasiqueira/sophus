package repo

import (
	"sophus/internal/objectcache"
	"strconv"
	"time"
)

func invalidateSessionCache() { objectcache.Invalidate("session") }
func invalidateAccessCache()  { objectcache.Invalidate("access") }
func invalidateSettingsCache() {
	objectcache.Invalidate("configuration")
	invalidateAccessCache()
	invalidateSessionCache()
}
func invalidateAvatarCache(id int) { objectcache.Invalidate("avatar-" + strconv.Itoa(id)) }

func GetAvatarContact(id int) (Contact, error) {
	var contact Contact
	err := objectcache.Get("avatar-"+strconv.Itoa(id), "contact", 30*time.Second, &contact, func() error {
		var err error
		contact, err = GetContactById(id)
		return err
	})
	return contact, err
}
