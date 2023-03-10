package handler

import (
	"HappyShopTogether/model"
	"HappyShopTogether/model/dbop"
	"HappyShopTogether/utils"
	"HappyShopTogether/utils/code"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
	"time"
)

type ShareBillCreateModel struct {
	ModeID    uint `json:"mode_id"`
	AddressID uint `json:"address_id"`
	Mode      bool `josn:"mode"`
}

type ShareBillJoinModel struct {
	ShareBillID string `json:"share_bill_id"`
	OwnerID     uint   `json:"owner_id"`
	AddressID   uint   `json:"address_id"`
}

// ShareBillDetailsModel share_bill
type ShareBillDetailsModel struct {
	CommodityInfo *model.CommodityInfo `json:"commodity_info"`
	CreateAt      time.Time            `json:"create_at"`
	DoneAt        time.Time            `json:"done_at"`
	Member        []*dbop.Member       `json:"member"`
	OwnerID       uint                 `json:"owner_id"`
	ShareBillID   string               `json:"share_bill_id"`
	ShopName      string               `json:"shop_name"`
	ShopAvatar    string               `json:"shop_avatar"`
	Status        uint8                `json:"status"`
}

func beforeCreateCheckCommodity(c *gin.Context, condition *model.CommodityInfo) (*model.CommodityInfo, bool) {
	commodity, msgCode, _ := dbop.CommodityInfoCheck(condition)

	if msgCode.Code == code.CheckError {
		code.GinServerError(c)
		return nil, false
	} else if msgCode.Code == code.DBEmpty {
		code.GinMissingItems(c)
		return nil, false
	}

	return commodity[0], true
}

func beforeCreateCheckAddress(c *gin.Context, condition *model.CustomerAddress) (*model.CustomerAddress, bool) {
	address, msgCode, _ := dbop.CustomerAddressCheck(condition)

	if msgCode.Code == code.CheckError {
		code.GinServerError(c)
		return nil, false
	} else if msgCode.Code == code.DBEmpty {
		code.GinUnMatchedID(c)
		return nil, false
	}

	if address[0].Default == model.AddressDelete {
		code.GinMissingAddress(c)
		return nil, false
	}

	return nil, true
}

func afterCreateCommodityUpdate(c *gin.Context, tx *gorm.DB, condition, update *model.CommodityInfo) bool {
	_, msgCode, _ := dbop.CommodityInfoUpdate(tx, condition, update)

	if msgCode.Code == code.UpdateError {
		tx.Rollback()
		code.GinServerError(c)
		return false
	} else if msgCode.Code == code.DBEmpty {
		tx.Rollback()
		code.GinUnMatchedID(c)
		return false
	}
	return true
}

/*
   6. ????????? ?????????????????????
   (1) ?????????????????? ?????????, ??????
   (2) ?????????, ??????????????????
   (3) ???????????? ????????????????????????
   (4) ??????????????????
   (5) ???????????????????????????????????????
*/

func shareBillTimeOverCheck(ID string) bool {
	afterShareBill, msgCode, _ := dbop.ShareBillCheck(&model.ShareBill{ID: ID})
	// ????????? ?????? ?????? ?????????
	if msgCode.Code == code.DBEmpty || afterShareBill[0].Status == model.ShareBillSuccess {
		return true
	}

	tx := model.Db.Self.Begin()

	// (2) ?????????, ??????????????????
	_, _, err := dbop.ShareBillUpdate(tx, &model.ShareBill{
		ID: ID,
	}, &model.ShareBill{
		Status:   model.ShareBillFailed,
		FinishAt: time.Now(),
	})

	if err != nil {
		return false
	}

	// (3) ???????????? ????????????????????????

	// get
	orders, msgCode2, _ := dbop.OrderCheck(&model.Order{
		ShareBillID: ID,
	})

	if msgCode2.Code == code.CheckError || msgCode2.Code == code.DBEmpty {
		tx.Rollback()
		return false
	}
	// update  ????????????
	for _, value := range orders {
		_, msgCode9, _ := dbop.OrderUpdate(tx, &model.Order{ID: value.ID}, &model.Order{
			Status:   model.OrderCancel,
			FinishAt: time.Now()})

		if msgCode9.Code == code.UpdateError {
			tx.Rollback()
			return false
		}
	}
	// (4) ??????????????????
	commodity, msgCode3, _ := dbop.CommodityInfoCheck(&model.CommodityInfo{ID: afterShareBill[0].CommodityID})

	if msgCode3.Code == code.CheckError {
		tx.Rollback()
		return false
	} else if msgCode3.Code == code.DBEmpty {
		tx.Rollback()
		return false
	}

	_, msgCode4, _ := dbop.CommodityInfoUpdate(tx,
		&model.CommodityInfo{ID: afterShareBill[0].CommodityID},
		&model.CommodityInfo{
			Count:  commodity[0].Count + uint(len(orders)),
			Status: CommodityStatusDecide(commodity[0].Status, true, commodity[0].Count+uint(len(orders))),
		})

	if msgCode4.Code == code.UpdateError {
		tx.Rollback()
		return false
	} else if msgCode4.Code == code.DBEmpty {
		tx.Rollback()
		return false
	}

	tx.Commit()

	return true
}

// ??????????????????????????? ????????????5s???????????????
func timerOverRepeatCheck(ID string) {
	if !shareBillTimeOverCheck(ID) {
		time.AfterFunc(5*time.Second, func() {
			timerOverRepeatCheck(ID)
		})
	}
}

// ShareBillCreateHandler ??????????????????
/*
   before
   1. ?????? ??????id ??????id ??????id??? ????????????

   2. ?????? ????????????
   3. ????????????
   4. ??????????????????

   after
   5. ??????????????????
   6. ????????? ?????????????????????
       (1) ?????????????????? ?????????, ??????
       (2) ?????????, ??????????????????
       (3) ???????????? ????????????????????????
       (4) ??????????????????
       (5) ???????????????????????????????????????

*/
func ShareBillCreateHandler(c *gin.Context) {
	ID, _, _ := utils.GetTokenInfo(c)

	shareBillCreateModel := &ShareBillCreateModel{}
	if !utils.QuickBind(c, shareBillCreateModel) {
		return
	}

	var cart *model.ShoppingCart

	// in cart
	if shareBillCreateModel.Mode {
		cart0, msgCode0, _ := dbop.ShoppingCartCheck(&model.ShoppingCart{ID: shareBillCreateModel.ModeID, CustomerID: ID})
		if msgCode0.Code == code.CheckError {
			code.GinServerError(c)
			return
		} else if msgCode0.Code == code.DBEmpty {
			code.GinMissingCart(c)
			return
		}
		cart = cart0[0]
	}

	var commodityID uint

	if shareBillCreateModel.Mode {
		commodityID = cart.CommodityID
	} else {
		commodityID = shareBillCreateModel.ModeID
	}

	// 1. ?????? ??????id ??????id ??????id??? ????????????
	// ??????????????????
	commodity, err := beforeCreateCheckCommodity(c, &model.CommodityInfo{ID: commodityID})
	if !err {
		return
	}

	if commodity.Status != model.CommodityStatusOnShelf && commodity.Status != model.CommodityStatusNotEnought {
		code.GinNotOnShelf(c)
		return
	} else if commodity.Status == model.CommodityStatusNotEnought {
		code.GinNotEnough(c)
		return
	}

	// ????????????
	_, err2 := beforeCreateCheckAddress(c, &model.CustomerAddress{
		ID:         shareBillCreateModel.AddressID,
		CustomerID: ID,
	})

	if !err2 {
		return
	}

	// ????????????

	tx := model.Db.Self.Begin()

	// 2. ?????? ????????????

	shareBill, msgCode2, _ := dbop.ShareBillCreate(tx, &model.ShareBill{
		ID:          utils.ShareBillIDGenerate(),
		OwnerID:     ID,
		CommodityID: commodity.ID,
		Status:      model.ShareBillWaitingForTwo,
		FinishAt:    time.Now(),
		Price:       commodity.Price,
	})

	if msgCode2.Code == code.InsertError || msgCode2.Code == code.DBEmpty {
		tx.Rollback()
		code.GinServerError(c)
		return
	}

	// 3. ????????????

	_, msgCode3, _ := dbop.ShareBillTeamCreate(tx, &model.ShareBillTeam{
		ShareBillID: shareBill.ID,
		MemberID:    ID,
	})

	if msgCode3.Code == code.InsertError || msgCode3.Code == code.DBEmpty {
		tx.Rollback()
		code.GinServerError(c)
		return
	}

	// 4. ??????????????????
	order, msgCode5, _ := dbop.OrderCreate(tx, &model.Order{
		ID:          utils.OrderIDGenerate(),
		ShareBillID: shareBill.ID,
		CustomerID:  ID,
		MerchantID:  commodity.MerchantID,
		AddressID:   shareBillCreateModel.AddressID,
		DueAt:       time.Now(),
		CommodityAt: time.Now(),
		FinishAt:    time.Now(),
		Status:      model.OrderCreated,
	})

	if msgCode5.Code == code.InsertError || msgCode5.Code == code.DBEmpty {
		tx.Rollback()
		code.GinServerError(c)
		return
	}

	// 5. ??????????????????
	if !afterCreateCommodityUpdate(c, tx, &model.CommodityInfo{ID: commodity.ID}, &model.CommodityInfo{
		Count:  commodity.Count - 1,
		Status: CommodityStatusDecide(commodity.Status, true, commodity.Count-1),
	}) {
		return
	}

	// ???????????????
	if shareBillCreateModel.Mode {
		msgCode6, _ := dbop.ShoppingCartDrop(tx, &model.ShoppingCart{ID: shareBillCreateModel.ModeID, CustomerID: ID})
		if msgCode6.Code == code.DropError {
			tx.Rollback()
			code.GinServerError(c)
			return
		} else if msgCode6.Code == code.DBEmpty {
			tx.Rollback()
			code.GinMissingCart(c)
			return
		}
	}

	tx.Commit()

	time.AfterFunc(time.Duration(utils.GlobalConfig.Global.GroupForMemberTime)*time.Hour, func() {
		// ?????????????????????
		timerOverRepeatCheck(shareBill.ID)
	})

	code.GinOKPayload(c, &gin.H{
		"share_bill_id": shareBill.ID,
		"order_id":      order.ID,
		"create_time":   shareBill.CreatedAt,
	})

}

/**

  1. ??????????????? ??????
  2. ??????????????????
  3. ????????????
  4. ????????????
  5. ????????????
  6. ????????????????????????????????????


*/

// ShareBillJoinHandler ????????????
func ShareBillJoinHandler(c *gin.Context) {
	ID, _, _ := utils.GetTokenInfo(c)

	shareBillJoinModel := &ShareBillJoinModel{}
	if !utils.QuickBind(c, shareBillJoinModel) {
		return
	}

	// 1. ??????????????? ??????
	shareBill, msgCode, _ := dbop.ShareBillCheck(&model.ShareBill{
		ID:      shareBillJoinModel.ShareBillID,
		OwnerID: shareBillJoinModel.OwnerID,
	})

	if msgCode.Code == code.CheckError {
		code.GinServerError(c)
		return
	} else if msgCode.Code == code.DBEmpty {
		code.GinUnMatchedID(c)
		return
	}

	if shareBill[0].Status == model.ShareBillFailed || shareBill[0].Status == model.ShareBillSuccess {
		code.GinShareBillDone(c)
		return
	}

	members, msgcode12, _ := dbop.ShareBillTeamCheck(&model.ShareBillTeam{ShareBillID: shareBillJoinModel.ShareBillID})

	if msgcode12.Code == code.CheckError || msgcode12.Code == code.DBEmpty {
		code.GinServerError(c)
		return
	}

	// ??????????????????
	for _, member := range members {
		if member.MemberID == ID {
			code.GinUserInTeam(c)
			return
		}
	}

	// ??????????????????
	commodity, err := beforeCreateCheckCommodity(c, &model.CommodityInfo{ID: shareBill[0].CommodityID})
	if !err {
		return
	}

	if commodity.Status != model.CommodityStatusOnShelf && commodity.Status != model.CommodityStatusNotEnought {
		code.GinNotOnShelf(c)
		return
	}

	// ????????????
	_, err2 := beforeCreateCheckAddress(c, &model.CustomerAddress{
		ID:         shareBillJoinModel.AddressID,
		CustomerID: ID,
	})

	if !err2 {
		return
	}

	// ??????????????????
	tx := model.Db.Self.Begin()

	// ????????????
	_, msgCode3, _ := dbop.ShareBillTeamCreate(tx, &model.ShareBillTeam{
		ShareBillID: shareBill[0].ID,
		MemberID:    ID,
	})

	if msgCode3.Code == code.InsertError || msgCode3.Code == code.DBEmpty {
		tx.Rollback()
		code.GinServerError(c)
		return
	}

	// ??????????????????
	_, msgCode5, _ := dbop.OrderCreate(tx, &model.Order{
		ID:          utils.OrderIDGenerate(),
		ShareBillID: shareBill[0].ID,
		CustomerID:  ID,
		MerchantID:  commodity.MerchantID,
		AddressID:   shareBillJoinModel.AddressID,
		DueAt:       time.Now(),
		CommodityAt: time.Now(),
		FinishAt:    time.Now(),
		Status:      model.OrderCreated,
	})

	if msgCode5.Code == code.InsertError || msgCode5.Code == code.DBEmpty {
		tx.Rollback()
		code.GinServerError(c)
		return
	}

	// ????????????
	_, msgCode6, _ := dbop.CommodityInfoUpdate(tx, &model.CommodityInfo{
		ID: shareBill[0].CommodityID,
	}, &model.CommodityInfo{
		Count:  commodity.Count - 1,
		Status: CommodityStatusDecide(commodity.Status, true, commodity.Count-1),
	})

	if msgCode6.Code == code.UpdateError {
		tx.Rollback()
		code.GinServerError(c)
		return
	} else if msgCode6.Code == code.DBEmpty {
		//		tx.Rollback()
		//		code.GinUnMatchedID(c)
		//		return
	}

	// ??????????????????
	if shareBill[0].Status == model.ShareBillWaitingForTwo {
		// ??????????????????
		_, msgCode7, _ := dbop.ShareBillUpdate(tx,
			&model.ShareBill{ID: shareBill[0].ID},
			&model.ShareBill{Status: model.ShareBillWaitingForOne})

		if msgCode7.Code == code.UpdateError || msgCode7.Code == code.DBEmpty {
			tx.Rollback()
			code.GinServerError(c)
			return
		}

	} else {
		// 5.??????????????????
		_, msgCode7, _ := dbop.ShareBillUpdate(tx,
			&model.ShareBill{ID: shareBill[0].ID},
			&model.ShareBill{Status: model.ShareBillSuccess, FinishAt: time.Now()})

		if msgCode7.Code == code.UpdateError || msgCode7.Code == code.DBEmpty {
			tx.Rollback()
			code.GinServerError(c)
			return
		}

		// 5. ????????????
		// get
		orders, msgCode8, _ := dbop.OrderCheck(&model.Order{
			ShareBillID: shareBill[0].ID,
		})

		if msgCode8.Code == code.CheckError || msgCode8.Code == code.DBEmpty {
			tx.Rollback()
			code.GinServerError(c)
			return
		}
		// update
		for _, value := range orders {
			_, msgCode9, _ := dbop.OrderUpdate(tx, &model.Order{ID: value.ID}, &model.Order{
				Status: model.OrderDue,
				DueAt:  time.Now()})

			if msgCode9.Code == code.UpdateError {
				tx.Rollback()
				code.GinServerError(c)
				return
			}
		}
	}

	tx.Commit()
	code.GinOKEmpty(c)
}

// ShareBillLink ??????????????????
//func ShareBillLink(c *gin.Context) {}

// ShareBillListHandler ??????????????????
func ShareBillListHandler(c *gin.Context) {
	ID, _, _ := utils.GetTokenInfo(c)

	limit := c.Query("limit")
	page := c.Query("page")

	shareBills, msgCode, _ := dbop.ShareBillLimitPageCheck(&model.ShareBill{
		OwnerID: ID,
	}, limit, page)

	if msgCode.Code == code.CheckError {
		code.GinServerError(c)
		return
	} else if msgCode.Code == code.DBEmpty {
		code.GinOKPayload(c, &gin.H{
			"list": []*ShareBillDetailsModel{},
		})
		return
	}

	var shareBillDetailModels []*ShareBillDetailsModel = make([]*ShareBillDetailsModel, len(shareBills))

	for index, shareBill := range shareBills {

		shareBillDetailSingle, msgCode2, _ := shareBillDetailSingleUnion(shareBill)

		if msgCode2.Code == code.CheckError || msgCode2.Code == code.DBEmpty {
			code.GinServerError(c)
			return
		}

		shareBillDetailModels[index] = shareBillDetailSingle

	}
	code.GinOKPayload(c, &gin.H{
		"list":  shareBillDetailModels,
		"count": len(shareBillDetailModels),
	})
}

// ShareBillDetailHandler ????????????????????????
func ShareBillDetailHandler(c *gin.Context) {
	ID, _, _ := utils.GetTokenInfo(c)

	pathStringIDModel := &PathStringIDModel{}
	if !utils.QuickBindPath(c, pathStringIDModel) {
		return
	}

	// ?????????????????????
	_, msgCode0, _ := dbop.ShareBillTeamCheck(&model.ShareBillTeam{
		ShareBillID: pathStringIDModel.ID,
		MemberID:    ID,
	})

	if msgCode0.Code == code.CheckError {
		code.GinServerError(c)
		return
	} else if msgCode0.Code == code.DBEmpty {
		code.GinUnAuthorized(c)
		return
	}

	// ?????????
	shareBills, msgCode, _ := dbop.ShareBillCheck(&model.ShareBill{
		ID: pathStringIDModel.ID,
	})

	if msgCode.Code == code.CheckError {
		code.GinServerError(c)
		return
	} else if msgCode.Code == code.DBEmpty {
		code.GinOKPayloadAny(c, &ShareBillDetailsModel{})
		return
	}

	shareBillDetailModel, msgCode2, _ := shareBillDetailSingleUnion(shareBills[0])

	if msgCode2.Code == code.CheckError || msgCode2.Code == code.DBEmpty {
		code.GinServerError(c)
		return
	}

	code.GinOKPayloadAny(c, shareBillDetailModel)
}

func shareBillDetailSingleUnion(shareBill *model.ShareBill) (*ShareBillDetailsModel, *code.MsgCode, bool) {
	commodity, msgCode2, _ := dbop.CommodityInfoCheck(&model.CommodityInfo{ID: shareBill.CommodityID})
	if msgCode2.Code == code.CheckError || msgCode2.Code == code.DBEmpty {
		return nil, msgCode2, false
	}

	members, msgCode3, _ := dbop.ShareBillTeamUnionCheck(&model.ShareBillTeam{ShareBillID: shareBill.ID})
	if msgCode3.Code == code.CheckError || msgCode3.Code == code.DBEmpty {
		return nil, msgCode3, false
	}

	merchant, msgCode4, _ := dbop.MerchantInfoCheck(&model.MerchantInfo{MerchantID: commodity[0].MerchantID})
	if msgCode4.Code == code.CheckError || msgCode4.Code == code.DBEmpty {
		return nil, msgCode4, false
	}

	shareBillDetailModels := &ShareBillDetailsModel{
		CommodityInfo: commodity[0],
		CreateAt:      shareBill.CreatedAt,
		DoneAt:        shareBill.FinishAt,
		Member:        members,
		OwnerID:       shareBill.OwnerID,
		ShareBillID:   shareBill.ID,
		ShopName:      merchant.ShopName,
		ShopAvatar:    merchant.ShopAvatar,
		Status:        shareBill.Status,
	}

	return shareBillDetailModels, &code.MsgCode{Msg: "OK", Code: code.OK}, true
}
