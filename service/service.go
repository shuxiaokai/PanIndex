package service

import (
	"encoding/base64"
	"errors"
	"fmt"
	"github.com/bluele/gcache"
	"github.com/gabriel-vasile/mimetype"
	"github.com/gin-gonic/gin"
	"github.com/libsgh/PanIndex/control/middleware"
	"github.com/libsgh/PanIndex/dao"
	"github.com/libsgh/PanIndex/module"
	"github.com/libsgh/PanIndex/pan"
	"github.com/libsgh/PanIndex/util"
	uuid "github.com/satori/go.uuid"
	log "github.com/sirupsen/logrus"
	"github.com/skip2/go-qrcode"
	"gorm.io/gorm"
	"io/ioutil"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

var UrlCache = gcache.New(5000).LRU().Build()
var FilesCache = gcache.New(5000).LRU().Build()
var FileCache = gcache.New(5000).LRU().Build()

func Index(ac module.Account, path, fullPath, sortColumn, sortOrder string, isView bool) ([]module.FileNode, bool, string, string) {
	fns := []module.FileNode{}
	isFile := false
	var err error
	if ac.CachePolicy == "nc" {
		fns, isFile, _ = GetFilesFromApi(ac, path, fullPath, "default", "null")
		fns = FilterHideFiles(fns)
	} else if ac.CachePolicy == "mc" {
		if FilesCache.Has(fullPath) {
			files, err := FilesCache.Get(fullPath)
			if err == nil {
				fcb := files.(FilesCacheBean)
				log.Debugf("get file from cache:%s", fullPath)
				fns = fcb.FileNodes
				isFile = fcb.IsFile
			}
		} else {
			fns, isFile, err = GetFilesFromApi(ac, path, fullPath, "default", "null")
			fns = FilterHideFiles(fns)
			cacheTime := time.Now().Format("2006-01-02 15:04:05")
			if err == nil {
				FilesCache.SetWithExpire(fullPath, FilesCacheBean{fns, isFile, cacheTime}, time.Hour*time.Duration(ac.ExpireTimeSpan))
			}
		}
	} else if ac.CachePolicy == "dc" {
		fns, isFile = GetFilesFromDb(ac, fullPath, "default", "null")
		fns = FilterHideFiles(fns)
	}
	sortFns := make([]module.FileNode, len(fns))
	copy(sortFns, fns)
	util.SortFileNode(sortColumn, sortOrder, sortFns)
	var lastFile, nextFile = "", ""
	if isView && isFile {
		lastFile, nextFile = GetLastNextFile(ac, path, fullPath, "default", "null")
	}
	return sortFns, isFile, lastFile, nextFile
}

type FilesCacheBean struct {
	FileNodes []module.FileNode
	IsFile    bool
	CacheTime string
}

type FileCacheBean struct {
	FileNode  module.FileNode
	CacheTime string
}

func GetFilesFromApi(ac module.Account, path, fullPath, sortColumn, sortOrder string) ([]module.FileNode, bool, error) {
	var fns []module.FileNode
	p, _ := pan.GetPan(ac.Mode)
	fileId := GetFileIdByPath(ac, path, fullPath)
	file, err := p.File(ac, fileId, fullPath)
	isFile := false
	if err != nil {
		log.Error(err)
	}
	if !file.IsFolder {
		//is file
		fns = append(fns, file)
		isFile = true
	} else {
		//is folder
		fns, err = p.Files(ac, fileId, fullPath, sortColumn, sortOrder)
		if err != nil {
			log.Error(err)
		}
	}
	return fns, isFile, err
}

func GetFilesFromDb(ac module.Account, path, sortColumn, sortOrder string) ([]module.FileNode, bool) {
	var fns []module.FileNode
	file, isFile := dao.FindFileByPath(ac, path)
	if isFile && file.FileId != "" && file.Id != "" {
		fns = append(fns, file)
	} else {
		isFile = false
		fns = dao.FindFileListByPath(ac, path, sortColumn, sortOrder)
	}
	return fns, isFile
}

func Search(searchKey string) []module.FileNode {
	var fns []module.FileNode
	//only support db cache mode
	sql := `select
				fn.*
			from
				file_node fn
			left join account a on
				fn.account_id = a.id
			where
				fn.file_name like ?`
	dao.DB.Raw(sql, "%"+searchKey+"%").First(&fns)
	return fns
}

func FilterHideFiles(files []module.FileNode) []module.FileNode {
	var fns []module.FileNode
	hideMap := dao.GetHideFilesMap()
	for _, file := range files {
		_, ok := hideMap[file.Path]
		if !ok {
			fns = append(fns, file)
		}
	}
	return fns
}

func FilterFilesByType(files []module.FileNode, viewType string) []module.FileNode {
	var fns []module.FileNode
	for _, file := range files {
		if file.ViewType == viewType {
			fns = append(fns, file)
		}
	}
	return fns
}

func HasParent(path string) (bool, string) {
	hasParent := false
	parentPath := ""
	if path != "/" {
		hasParent = true
	}
	parentPath = util.GetParentPath(path)
	return hasParent, parentPath
}

func GetFileIdByPath(ac module.Account, path, fullPath string) string {
	fileId := ac.RootId
	if path == "/" || path == "" {
		return fileId
	}
	if ac.CachePolicy == "dc" {
		fileId = dao.GetFileIdByPath(ac.Id, fullPath)
		return fileId
	} else if ac.CachePolicy == "mc" {
		parentPath := util.GetParentPath(fullPath)
		if FilesCache.Has(parentPath) {
			files, err := FilesCache.Get(parentPath)
			if err == nil {
				fcb := files.(FilesCacheBean)
				if len(fcb.FileNodes) > 0 {
					for _, fn := range fcb.FileNodes {
						if fn.Path == fullPath {
							return fn.FileId
						}
					}
				}
			}
		}
	}
	paths := util.GetPrePath(path)
	for _, pathMap := range paths {
		fId, ok := LoopGetFileId(ac, fileId, pathMap["PathUrl"], path)
		fileId = fId
		if ok {
			break
		}
	}
	return fileId
}

func LoopGetFileId(ac module.Account, fileId, path, filePath string) (string, bool) {
	fileName := util.GetFileName(path)
	p, _ := pan.GetPan(ac.Mode)
	fns, _ := p.Files(ac, fileId, util.GetParentPath(path), "", "")
	fId, fnPath := GetCurrentId(fileName, fns)
	if fId != "" {
		if fnPath == filePath {
			return fId, true
		}
		return fId, false
	} else {
		return "", false
	}
}

func GetFileIdFromApi(ac module.Account, path string) string {
	fileId := ac.RootId
	paths := util.GetPrePath(path)
	for _, pathMap := range paths {
		fId, ok := LoopGetFileId(ac, fileId, pathMap["PathUrl"], path)
		fileId = fId
		if ok {
			break
		}
	}
	return fileId
}

func GetCurrentId(pathName string, fns []module.FileNode) (string, string) {
	for _, fn := range fns {
		if fn.FileName == pathName {
			return fn.FileId, fn.Path
		}
	}
	return "", ""
}

type DownLock struct {
	FileId string
	L      *sync.Mutex
}

var dls = sync.Map{}

func GetDownloadUrl(ac module.Account, fileId string) string {
	var dl = DownLock{}
	if _, ok := dls.Load(fileId); ok {
		ss, _ := dls.Load(fileId)
		dl = ss.(DownLock)
	} else {
		dl.FileId = fileId
		dl.L = new(sync.Mutex)
		dls.LoadOrStore(fileId, dl)
	}
	downUrl := dl.GetDownlaodUrl(ac, fileId)
	return downUrl
}

func (dl *DownLock) GetDownlaodUrl(account module.Account, fileId string) string {
	var downloadUrl = ""
	if UrlCache.Has(account.Id + fileId) {
		cachUrl, err := UrlCache.Get(account.Id + fileId)
		if err == nil {
			downloadUrl = cachUrl.(string)
			log.Debugf("get download url from cache:%s", downloadUrl)
		}
	} else {
		p, _ := pan.GetPan(account.Mode)
		url, err := p.GetDownloadUrl(account, fileId)
		if err != nil {
			log.Error(err)
		}
		downloadUrl = url
		if downloadUrl != "" {
			if account.Mode == "aliyundrive" {
				UrlCache.SetWithExpire(account.Id+fileId, downloadUrl, time.Minute*230)
			} else {
				UrlCache.SetWithExpire(account.Id+fileId, downloadUrl, time.Minute*14)
			}
			log.Debugf("get download url from api:" + downloadUrl)
		}
	}
	return downloadUrl
}

//clear file memory cache
func ClearFileCache(p string) {
	keys := FilesCache.Keys(false)
	for _, key := range keys {
		k := key.(string)
		if strings.HasPrefix(k, p) {
			FilesCache.Remove(k)
		}
	}
}

//upload file
func Upload(accountId, p string, c *gin.Context) string {
	_, fullPath, path, _ := middleware.ParseFullPath(p)
	form, _ := c.MultipartForm()
	files := form.File["uploadFile"]
	account := module.Account{}
	result := dao.DB.Raw("select * from account where id=?", accountId).Take(&account)
	if errors.Is(result.Error, gorm.ErrRecordNotFound) {
		return "指定的账号不存在"
	}
	pan, _ := pan.GetPan(account.Mode)
	fileId := GetFileIdByPath(account, path, fullPath)
	if fileId == "" {
		return "指定目录不存在"
	}
	fileInfos := []*module.UploadInfo{}
	for _, file := range files {
		fileContent, _ := file.Open()
		byteContent, _ := ioutil.ReadAll(fileContent)
		fileInfos = append(fileInfos, &module.UploadInfo{
			FileName:    file.Filename,
			FileSize:    file.Size,
			ContentType: file.Header.Get("Content-Type"),
			Content:     byteContent,
		})
	}
	ok, r, err := pan.UploadFiles(account, fileId, fileInfos, true)
	if ok && err == nil {
		log.Debug(r)
		return "上传成功"
	} else {
		log.Debug(r)
		return "上传失败"
	}
}

//sync file cache
func Async(accountId, path string) string {
	account := module.Account{}
	result := dao.DB.Raw("select * from account where id=?", accountId).Take(&account)
	if errors.Is(result.Error, gorm.ErrRecordNotFound) {
		return "指定的账号不存在"
	}
	account.SyncDir = path
	account.SyncChild = 0
	if account.CachePolicy == "dc" {
		dao.SyncFilesCache(account)
	} else {
		ClearFileCache(path)
	}
	return "刷新成功"
}

//short url & qrcode
func ShortInfo(accountId, path, prefix, fileType string) (string, string, string) {
	si := module.ShareInfo{}
	dao.DB.Raw("select * from share_info where account_id = ? and file_path=?", accountId, path).First(&si)
	shortUrl := ""
	if accountId == "" || path == "" {
		return "", "", "无效的id"
	}
	shortCode := ""
	isFile := false
	if fileType == "1" {
		isFile = true
	}
	if si.ShortCode != "" {
		shortCode = si.ShortCode
	} else {
		shortCodes, err := util.Transform(accountId + path)
		if err != nil {
			log.Errorln(err)
			return "", "", "短链生成失败"
		}
		shortCode = shortCodes[0]
		dao.DB.Create(module.ShareInfo{
			accountId, path, shortCode, isFile,
		})
	}
	shortUrl = prefix + shortCode
	png, err := qrcode.Encode(shortUrl, qrcode.Medium, 256)
	if err != nil {
		panic(err)
	}
	dataURI := "data:image/png;base64," + base64.StdEncoding.EncodeToString([]byte(png))
	return shortUrl, dataURI, "短链生成成功"
}

//get file data
func GetFileData(account module.Account, downUrl, r string) ([]byte, string, int) {
	client := httpClient(r)
	req, _ := http.NewRequest("GET", downUrl, nil)
	req.Header.Add("Range", r)
	resp, err := client.Do(req)
	if err != nil {
		log.Errorln(err)
	}
	defer resp.Body.Close()
	data, _ := ioutil.ReadAll(resp.Body)
	mtype := mimetype.Detect(data)
	return data, mtype.String(), resp.StatusCode
}

func httpClient(r string) *http.Client {
	client := http.Client{
		CheckRedirect: func(r *http.Request, via []*http.Request) error {
			r.URL.Opaque = r.URL.Path
			return nil
		},
	}
	return &client
}

func AccountsToNodes() []module.FileNode {
	fns := []module.FileNode{}
	ids := map[string]string{}
	for _, bypass := range module.GloablConfig.BypassList {
		fn := module.FileNode{
			FileId:     fmt.Sprintf("/%s", bypass.Name),
			IsFolder:   true,
			FileName:   bypass.Name,
			FileSize:   0,
			SizeFmt:    "-",
			FileType:   "",
			Path:       fmt.Sprintf("/%s", bypass.Name),
			ViewType:   "",
			LastOpTime: "",
			ParentId:   "",
		}
		fns = append(fns, fn)
		for _, ac := range bypass.Accounts {
			ids[ac.Id] = ac.Id
		}
	}
	for _, account := range module.GloablConfig.Accounts {
		_, exists := ids[account.Id]
		if !exists {
			fn := module.FileNode{
				FileId:     fmt.Sprintf("/%s", account.Name),
				IsFolder:   true,
				FileName:   account.Name,
				FileSize:   int64(account.FilesCount),
				SizeFmt:    "-",
				FileType:   "",
				Path:       fmt.Sprintf("/%s", account.Name),
				ViewType:   "",
				LastOpTime: account.LastOpTime,
				ParentId:   "",
			}
			fns = append(fns, fn)
		}
	}
	return fns
}

func GetRedirectUri(shorCode string) string {
	redirectUri := "/"
	si := module.ShareInfo{}
	result := dao.DB.Raw("select * from share_info where short_code=?", shorCode).First(&si)
	if !errors.Is(result.Error, gorm.ErrRecordNotFound) {
		if !errors.Is(result.Error, gorm.ErrRecordNotFound) {
			if !si.IsFile {
				redirectUri = si.FilePath
			} else if si.IsFile && module.GloablConfig.EnablePreview == "0" {
				redirectUri = si.FilePath
			} else {
				redirectUri = si.FilePath + "?v"
			}

		}
	}
	return redirectUri
}

//path: filePath
func GetLastNextFile(ac module.Account, path, fullPath, sortColumn, sortOrder string) (string, string) {
	var fns []module.FileNode
	var err error
	parentPath := util.GetParentPath(path)
	fileFullPath := fullPath
	fullPath = util.GetParentPath(fullPath)
	//get from cache first
	if FilesCache.Has(fullPath) {
		files, err := FilesCache.Get(fullPath)
		if err == nil {
			fcb := files.(FilesCacheBean)
			log.Debugf("get file from cache:%s", fullPath)
			fns = fcb.FileNodes
		}
	} else {
		if ac.CachePolicy == "dc" {
			fns, _ = GetFilesFromDb(ac, fullPath, "default", "null")
		} else {
			fns, _, err = GetFilesFromApi(ac, parentPath, fullPath, sortColumn, sortOrder)
			fns = FilterHideFiles(fns)
			if err == nil {
				cacheTime := time.Now().Format("2006-01-02 15:04:05")
				FilesCache.SetWithExpire(fullPath, FilesCacheBean{fns, false, cacheTime}, time.Hour*time.Duration(ac.ExpireTimeSpan))
			}
		}
	}
	util.SortFileNode(sortColumn, sortOrder, fns)
	var lastFile, nextFile = "", ""
	for i, fn := range fns {
		if fn.Path == fileFullPath && i > 0 {
			if !fns[i-1].IsFolder {
				lastFile = fns[i-1].Path
			}
		}
		if fn.Path == fileFullPath && i < len(fns)-1 {
			if !fns[i+1].IsFolder {
				nextFile = fns[i+1].Path
			}
		}
	}
	return lastFile, nextFile
}

func GetFiles(ac module.Account, path, fullPath, sortColumn, sortOrder, viewType string) []module.FileNode {
	var fns []module.FileNode
	var err error
	//get from cache first
	if FilesCache.Has(fullPath) {
		files, err := FilesCache.Get(fullPath)
		if err == nil {
			fcb := files.(FilesCacheBean)
			log.Debugf("get file from cache:%s", fullPath)
			fns = fcb.FileNodes
		}
	} else {
		if ac.CachePolicy == "dc" {
			fns, _ = GetFilesFromDb(ac, fullPath, "default", "null")
		} else {
			fns, _, err = GetFilesFromApi(ac, path, fullPath, sortColumn, sortOrder)
			fns = FilterHideFiles(fns)
			if err == nil {
				cacheTime := time.Now().Format("2006-01-02 15:04:05")
				FilesCache.SetWithExpire(fullPath, FilesCacheBean{fns, false, cacheTime}, time.Hour*time.Duration(ac.ExpireTimeSpan))
			}
		}
	}
	fns = FilterFilesByType(fns, viewType)
	util.SortFileNode(sortColumn, sortOrder, fns)
	return fns
}

func GetCacheData(pathEsc string) []module.Cache {
	cache := []module.Cache{}
	fns := []module.FileNode{}
	if pathEsc != "" {
		dao.DB.Raw("select * from file_node where is_delete = 0 and path like ?", "%"+pathEsc+"%").Find(&fns)
	} else {
		dao.DB.Raw("select * from file_node where is_delete = 0 limit 100").Find(&fns)
	}
	for _, fn := range fns {
		cache = append(cache, module.Cache{fn.Path, fn.CreateTime, "DB", fn})
	}
	filesCache := FilesCache.GetALL(false)
	for filePath, data := range filesCache {
		fc := data.(FilesCacheBean)
		if pathEsc != "" {
			if strings.Contains(filePath.(string), pathEsc) {
				cache = append(cache, module.Cache{filePath.(string), fc.CacheTime, "Memory", fc.FileNodes})
			}
		} else {
			cache = append(cache, module.Cache{filePath.(string), fc.CacheTime, "Memory", fc.FileNodes})
		}
	}
	fileCache := FileCache.GetALL(false)
	for filePath, data := range fileCache {
		fc := data.(FileCacheBean)
		if pathEsc != "" {
			if strings.Contains(filePath.(string), pathEsc) {
				cache = append(cache, module.Cache{filePath.(string), fc.CacheTime, "Memory", fc.FileNode})
			}
		} else {
			cache = append(cache, module.Cache{filePath.(string), fc.CacheTime, "Memory", fc.FileNode})
		}
	}
	return cache
}

func GetCacheByPath(path string) []module.Cache {
	cache := []module.Cache{}
	fn := module.FileNode{}
	dao.DB.Raw("select * from file_node where is_delete = 0 and path=? limit 100", path).First(&fn)
	if fn.Path != "" {
		cache = append(cache, module.Cache{fn.Path, fn.CreateTime, "DB", fn})
	}
	fileCache, err := FilesCache.Get(path)
	if err == nil {
		fc := fileCache.(FilesCacheBean)
		cache = append(cache, module.Cache{path, fc.CacheTime, "Memory", fc.FileNodes})
	}
	return cache
}

func CacheClear(path string, isLoopChildren string) {
	if isLoopChildren == "0" {
		keys := FilesCache.Keys(false)
		for _, key := range keys {
			if key.(string) == path || strings.HasPrefix(key.(string)+"/", path) {
				FilesCache.Remove(key)
			}
		}
	} else {
		FilesCache.Remove(path)
	}
	accounts, cachePath := dao.FindAccountsByPath(path)
	for _, account := range accounts {
		account.SyncDir = cachePath
		if isLoopChildren == "0" {
			account.SyncChild = 0
		} else {
			account.SyncChild = 1
		}
		go dao.SyncFilesCache(account)
	}
}

func GetBypassByAccountId(accountId string) module.Bypass {
	bypass := module.Bypass{}
	dao.DB.Raw(`select
						b.*
					from
						bypass_accounts ba
					left join bypass b on
						ba.bypass_id = b.id
					where
						ba.account_id = ?`, accountId).Find(&bypass)
	return bypass
}

func UpdateCache(account module.Account, cachePath string) string {
	msg := "缓存清理成功"
	if account.CachePolicy == "nc" {
		msg = "当前网盘无需刷新操作！"
	} else if account.CachePolicy == "mc" {
		ClearFileCache(cachePath)
	} else {
		if account.Status == -1 {
			msg = "目录缓存中，请勿重复操作！"
		} else {
			account.SyncDir = cachePath
			account.SyncChild = 0
			dao.DB.Table("account").Where("id=?", account.Id).UpdateColumn("status", -1)
			go dao.SyncFilesCache(account)
			msg = "正在缓存目录，请稍后刷新页面查看缓存结果！"
		}
	}
	return msg
}

func GetAccounts() []module.Account {
	accounts := []module.Account{}
	bacIds := []string{}
	bypasses := dao.GetBypassList()
	if len(bypasses) > 0 {
		for _, bypass := range bypasses {
			ac := module.Account{}
			ac.Name = bypass.Name
			ac.Mode = "native"
			accounts = append(accounts, ac)
			if len(bypass.Accounts) > 0 {
				for _, bac := range bypass.Accounts {
					bacIds = append(bacIds, bac.Id)
				}
			}
		}
	}
	if len(module.GloablConfig.Accounts) > 0 {
		for _, account := range module.GloablConfig.Accounts {
			f := false
			if len(bacIds) > 0 {
				for _, id := range bacIds {
					if id == account.Id {
						f = true
					}
				}
			}
			if !f {
				accounts = append(accounts, account)
			}
		}
	}
	return accounts
}

func Files(ac module.Account, path, fullPath string) []module.FileNode {
	fns := []module.FileNode{}
	isFile := false
	var err error
	if ac.CachePolicy == "nc" {
		fns, isFile, _ = GetFilesFromApi(ac, path, fullPath, "default", "null")
		fns = FilterHideFiles(fns)
	} else if ac.CachePolicy == "mc" {
		if FilesCache.Has(fullPath) {
			files, err := FilesCache.Get(fullPath)
			if err == nil {
				fcb := files.(FilesCacheBean)
				log.Debugf("get file from cache:%s", fullPath)
				fns = fcb.FileNodes
				isFile = fcb.IsFile
			}
		} else {
			fns, isFile, err = GetFilesFromApi(ac, path, fullPath, "default", "null")
			if err == nil {
				fns = FilterHideFiles(fns)
				cacheTime := time.Now().Format("2006-01-02 15:04:05")
				FilesCache.SetWithExpire(fullPath, FilesCacheBean{fns, isFile, cacheTime}, time.Hour*time.Duration(ac.ExpireTimeSpan))
			}
		}
	} else if ac.CachePolicy == "dc" {
		fns, isFile = GetFilesFromDb(ac, fullPath, "default", "null")
		fns = FilterHideFiles(fns)
	}

	return fns
}

func DeleteFile(ac module.Account, path, fullPath string) error {
	//1. delete file from disk
	disk, _ := pan.GetPan(ac.Mode)
	fileId := GetFileIdByPath(ac, path, fullPath)
	_, _, err := disk.Remove(ac, fileId)
	//2. delete file from cache
	if err == nil {
		FileCache.Remove(fullPath)
		FilesCache.Remove(fullPath)
		FilesCache.Remove(util.GetParentPath(fullPath))
		dao.DeleteFileNodes(ac.Id, fileId)
	}
	return err
}

func GetFileFromApi(ac module.Account, path, fullPath string) (module.FileNode, error) {
	p, _ := pan.GetPan(ac.Mode)
	fileId := GetFileIdByPath(ac, path, fullPath)
	file, err := p.File(ac, fileId, fullPath)
	return file, err
}

func File(ac module.Account, path, fullPath string) (module.FileNode, error) {
	if ac.CachePolicy == "nc" {
		return GetFileFromApi(ac, path, fullPath)
	} else if ac.CachePolicy == "mc" {
		if FileCache.Has(fullPath) {
			files, err := FileCache.Get(fullPath)
			if err == nil {
				fcb := files.(FileCacheBean)
				log.Debugf("get file from cache:%s", fullPath)
				return fcb.FileNode, nil
			}
		} else {
			fn, err := GetFileFromApi(ac, path, fullPath)
			cacheTime := time.Now().Format("2006-01-02 15:04:05")
			FileCache.SetWithExpire(fullPath, FileCacheBean{fn, cacheTime}, time.Hour*time.Duration(ac.ExpireTimeSpan))
			return fn, err
		}
	} else if ac.CachePolicy == "dc" {
		fn, _ := dao.FindFileByPath(ac, fullPath)
		return fn, nil
	}
	return module.FileNode{}, nil
}

//webdav upload callback
func UploadCall(account module.Account, file module.FileNode, overwrite bool) {
	if account.CachePolicy == "mc" {
		parentPath := util.GetParentPath(file.Path)
		FilesCache.Remove(parentPath)
		FileCache.Remove(file.Path)
	} else if account.CachePolicy == "dc" {
		file.Id = uuid.NewV4().String()
		file.IsDelete = 0
		fn := module.FileNode{}
		exist := false
		result := dao.DB.Table("file_node").
			Where("path=?", file.Path).Take(&fn)
		if !errors.Is(result.Error, gorm.ErrRecordNotFound) {
			exist = true
		}
		if overwrite && exist {
			dao.DB.Table("file_node").
				Where("path=?", file.Path).
				Update("last_op_time", file.LastOpTime).
				Update("file_size", file.FileSize).
				Update("size_fmt", util.FormatFileSize(file.FileSize))
		} else {
			dao.DB.Create(&file)
		}
	}
}

//webdav mkdir callback
func MkdirCall(account module.Account, file module.FileNode) {
	UploadCall(account, file, false)
}

//webdav move callback
func MoveCall(account module.Account, fileId, srcFullPath, dstFullPath string) {
	UrlCache.Remove(account.Id + fileId)
	if account.CachePolicy == "mc" {
		parentPath := util.GetParentPath(srcFullPath)
		dstParentPath := util.GetParentPath(dstFullPath)
		FilesCache.Remove(parentPath)
		FilesCache.Remove(dstParentPath)
		FileCache.Remove(dstFullPath)
		FileCache.Remove(srcFullPath)
	} else if account.CachePolicy == "dc" {
		fn := module.FileNode{}
		dao.DB.Where("path=?", srcFullPath).First(&fn)
		p, _ := pan.GetPan(account.Mode)
		newFn, _ := p.File(account, fileId, dstFullPath)
		fn.LastOpTime = newFn.LastOpTime
		fn.Path = newFn.Path
		fn.ParentPath = newFn.ParentPath
		fn.FileId = newFn.FileId
		fn.ParentId = newFn.ParentId
		fn.FileName = newFn.FileName
		dao.DB.Model(&module.FileNode{}).Where("id=?", fn.Id).Updates(fn)
		if fn.IsFolder {
			//TODO if file_id is path may not work
			account.SyncDir = newFn.Path
			account.SyncChild = 0
			dao.SyncFilesCache(account)
		}
	}
}

func FtpDownload(ac module.Account, downUrl string, fileNode module.FileNode, c *gin.Context) {
	p, _ := pan.GetPan(ac.Mode)
	statusCode := http.StatusOK
	if c.GetHeader("Range") != "" {
		statusCode = http.StatusPartialContent
	}
	r, err := p.(*pan.FTP).ReadFileReader(ac, downUrl, 0)
	defer r.Close()
	if err == nil {
		fileName := url.QueryEscape(fileNode.FileName)
		extraHeaders := map[string]string{
			"Content-Disposition": `attachment; filename="` + fileName + `"`,
			"Accept-Ranges":       "bytes",
			"Content-Range":       fmt.Sprintf("bytes %d-%d/%d", 0, fileNode.FileSize-1, fileNode.FileSize),
		}
		c.DataFromReader(statusCode, fileNode.FileSize,
			util.GetMimeTypeByExt(fileNode.FileType), r, extraHeaders)
	}
}

func WebDavDownload(ac module.Account, downUrl string, fileNode module.FileNode, c *gin.Context) {
	p, _ := pan.GetPan(ac.Mode)
	statusCode := http.StatusOK
	if c.GetHeader("Range") != "" {
		statusCode = http.StatusPartialContent
	}
	r, err := p.(*pan.WebDav).ReadFileReader(ac, downUrl, 0, fileNode.FileSize)
	defer r.Close()
	if err == nil {
		fileName := url.QueryEscape(fileNode.FileName)
		extraHeaders := map[string]string{
			"Content-Disposition": `attachment; filename="` + fileName + `"`,
			"Accept-Ranges":       "bytes",
			"Content-Range":       fmt.Sprintf("bytes %d-%d/%d", 0, fileNode.FileSize-1, fileNode.FileSize),
		}
		c.DataFromReader(statusCode, fileNode.FileSize,
			util.GetMimeTypeByExt(fileNode.FileType), r, extraHeaders)
	}
}
